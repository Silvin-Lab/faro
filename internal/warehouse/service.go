package warehouse

import (
	"context"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Service expone la lógica del almacén: proveedores, existencias + mín/máx, y los
// tres flujos del ledger (compra, salida, merma). Todo tenant-scoped; el gating a
// super_admin lo aplica el router (server.go).
type Service struct {
	store *store
	now   func() time.Time // inyectable en tests; nil => time.Now
}

func NewService(pool *pgxpool.Pool) *Service {
	return &Service{store: newStore(pool)}
}

func (svc *Service) nowUTC() time.Time {
	if svc.now != nil {
		return svc.now()
	}
	return time.Now()
}

// ---- Proveedores -----------------------------------------------------------

// SupplierInput agrupa los datos de alta/edición de un proveedor. Los opcionales
// van como puntero (nil = ausente en el PATCH / no capturado en el POST).
type SupplierInput struct {
	Name    string
	Address *string
	Email   *string
	Phone   *string
}

func (svc *Service) CreateSupplier(ctx context.Context, tenantID string, in SupplierInput) (Supplier, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return Supplier{}, ErrValidation
	}
	return svc.store.createSupplier(ctx, tenantID, name, normalize(in.Address), normalize(in.Email), normalize(in.Phone))
}

func (svc *Service) ListSuppliers(ctx context.Context, tenantID string, status *string) ([]Supplier, error) {
	if status != nil {
		s := strings.TrimSpace(*status)
		if s != "active" && s != "inactive" {
			return nil, ErrValidation
		}
		status = &s
	}
	return svc.store.listSuppliers(ctx, tenantID, status)
}

// SupplierUpdate agrupa los cambios parciales (nil = no cambia).
type SupplierUpdate struct {
	Name    *string
	Address *string
	Email   *string
	Phone   *string
	Status  *string
}

func (svc *Service) UpdateSupplier(ctx context.Context, tenantID, id string, in SupplierUpdate) (Supplier, error) {
	if in.Name != nil {
		n := strings.TrimSpace(*in.Name)
		if n == "" {
			return Supplier{}, ErrValidation
		}
		in.Name = &n
	}
	if in.Status != nil && *in.Status != "active" && *in.Status != "inactive" {
		return Supplier{}, ErrValidation
	}
	return svc.store.updateSupplier(ctx, tenantID, id, in.Name, in.Address, in.Email, in.Phone, in.Status)
}

// ---- Stock del almacén + mín/máx -------------------------------------------

// ListStock devuelve las existencias del almacén. status opcional (nil = todos,
// incluye insumos dados de baja que aún tengan stock); "active"/"inactive" filtran
// por estado del insumo (el select de "nuevo insumo para compra" usa ?status=active
// para no ofrecer insumos dados de baja). status inválido => ErrValidation.
func (svc *Service) ListStock(ctx context.Context, tenantID string, status *string) ([]WarehouseStockItem, error) {
	if status != nil {
		s := strings.TrimSpace(*status)
		if s != "active" && s != "inactive" {
			return nil, ErrValidation
		}
		status = &s
	}
	return svc.store.listStock(ctx, tenantID, status)
}

func (svc *Service) ToBuy(ctx context.Context, tenantID string) ([]ToBuyItem, error) {
	return svc.store.toBuy(ctx, tenantID)
}

// MinMaxUpdate expresa el cambio de mín/máx con PRESENCIA: SetMin/SetMax indican si
// el campo vino en el body; Min/Max su valor (nil = null = sin límite). Permite
// setear uno sin conocer el otro (§4.1); la validación max>=min usa el estado
// combinado con lo persistido.
type MinMaxUpdate struct {
	SetMin bool
	Min    *int
	SetMax bool
	Max    *int
}

// UpdateMinMax valida (min/max >= 0 y max >= min combinado) y hace el upsert lazy.
// Supply ajeno/inexistente => ErrNotFound.
func (svc *Service) UpdateMinMax(ctx context.Context, tenantID, supplyID string, in MinMaxUpdate) (WarehouseStockItem, error) {
	if in.SetMin && in.Min != nil && *in.Min < 0 {
		return WarehouseStockItem{}, ErrValidation
	}
	if in.SetMax && in.Max != nil && *in.Max < 0 {
		return WarehouseStockItem{}, ErrValidation
	}
	ok, err := svc.supplyOK(ctx, tenantID, supplyID)
	if err != nil {
		return WarehouseStockItem{}, err
	}
	if !ok {
		return WarehouseStockItem{}, ErrNotFound
	}
	return svc.store.upsertMinMax(ctx, tenantID, supplyID, in)
}

// ---- Ajuste manual de existencia -------------------------------------------

// AdjustInput fija la existencia TOTAL deseada del almacén para un insumo (NO un
// delta: el usuario escribe "cuánto tengo ahorita"). Date opcional (backdating),
// igual que compra/salida/merma.
type AdjustInput struct {
	NewQuantity int
	Date        string
}

// AdjustStock fija la existencia del almacén al total deseado registrando un
// movimiento 'adjustment' con la diferencia firmada contra el stock actual. Valida
// NewQuantity >= 0 (ErrValidation) e insumo del tenant (ErrNotFound). Devuelve el
// movimiento (nil si el valor no cambió: no-op) y el nuevo stock_base.
func (svc *Service) AdjustStock(ctx context.Context, tenantID, supplyID string, in AdjustInput, createdBy string) (*WarehouseMovement, int, error) {
	if in.NewQuantity < 0 {
		return nil, 0, ErrValidation
	}
	supplyID = strings.TrimSpace(supplyID)
	ok, err := svc.supplyOK(ctx, tenantID, supplyID)
	if err != nil {
		return nil, 0, err
	}
	if !ok {
		return nil, 0, ErrNotFound
	}
	createdAt := resolveCreatedAt(in.Date, svc.nowUTC())
	return svc.store.insertAdjustment(ctx, tenantID, supplyID, in.NewQuantity, createdBy, createdAt)
}

// ---- Compras ---------------------------------------------------------------

// PurchaseInput es la petición de una compra. Packages > 0, UnitCostCents >= 0.
type PurchaseInput struct {
	SupplyID      string
	SupplierID    string
	Packages      int
	UnitCostCents int
	Date          string // opcional (YYYY-MM-DD); vacío/hoy => now()
}

func (svc *Service) CreatePurchase(ctx context.Context, tenantID string, in PurchaseInput, createdBy string) (WarehouseMovement, int, error) {
	if in.Packages <= 0 || in.UnitCostCents < 0 {
		return WarehouseMovement{}, 0, ErrValidation
	}
	packageContent, ok, err := svc.store.supplyInfo(ctx, tenantID, strings.TrimSpace(in.SupplyID))
	if err != nil {
		return WarehouseMovement{}, 0, err
	}
	if !ok {
		return WarehouseMovement{}, 0, ErrInvalidSupply
	}
	supplierID := strings.TrimSpace(in.SupplierID)
	if supplierID == "" {
		return WarehouseMovement{}, 0, ErrValidation
	}
	okSup, err := svc.store.supplierInTenant(ctx, tenantID, supplierID)
	if err != nil {
		return WarehouseMovement{}, 0, err
	}
	if !okSup {
		return WarehouseMovement{}, 0, ErrInvalidSupplier
	}
	quantityBase := in.Packages * packageContent
	createdAt := resolveCreatedAt(in.Date, svc.nowUTC())
	return svc.store.insertPurchase(ctx, tenantID, in.SupplyID, supplierID, in.Packages, in.UnitCostCents, quantityBase, createdBy, createdAt)
}

func (svc *Service) ListPurchases(ctx context.Context, tenantID string, from, to *time.Time) ([]PurchaseItem, error) {
	return svc.store.listPurchases(ctx, tenantID, from, to)
}

// ---- Salidas ---------------------------------------------------------------

// DispatchInput es la petición de una salida. QuantityBase > 0 (unidad base).
type DispatchInput struct {
	SupplyID     string
	BranchID     string
	QuantityBase int
	Date         string
}

func (svc *Service) CreateDispatch(ctx context.Context, tenantID string, in DispatchInput, createdBy string) (WarehouseMovement, int, int, error) {
	if in.QuantityBase <= 0 {
		return WarehouseMovement{}, 0, 0, ErrValidation
	}
	ok, err := svc.supplyOK(ctx, tenantID, strings.TrimSpace(in.SupplyID))
	if err != nil {
		return WarehouseMovement{}, 0, 0, err
	}
	if !ok {
		return WarehouseMovement{}, 0, 0, ErrInvalidSupply
	}
	branchID := strings.TrimSpace(in.BranchID)
	if branchID == "" {
		return WarehouseMovement{}, 0, 0, ErrInvalidBranch
	}
	okBranch, err := svc.store.branchInTenant(ctx, tenantID, branchID)
	if err != nil {
		return WarehouseMovement{}, 0, 0, err
	}
	if !okBranch {
		return WarehouseMovement{}, 0, 0, ErrInvalidBranch
	}
	createdAt := resolveCreatedAt(in.Date, svc.nowUTC())
	return svc.store.insertDispatch(ctx, tenantID, in.SupplyID, branchID, in.QuantityBase, createdBy, createdAt)
}

func (svc *Service) ListDispatches(ctx context.Context, tenantID string, from, to *time.Time) ([]DispatchItem, error) {
	return svc.store.listDispatches(ctx, tenantID, from, to)
}

// ---- Mermas ----------------------------------------------------------------

// WasteInput es la petición de una merma. QuantityBase > 0, Reason obligatorio.
// BranchID opcional (nil/"" = merma en el almacén central); si viene, del tenant.
type WasteInput struct {
	SupplyID     string
	QuantityBase int
	Reason       string
	BranchID     *string
	Date         string
}

// CreateWaste bifurca según BranchID (§3.3): sin sucursal descuenta el almacén; con
// sucursal descuenta esa sucursal y NO toca el almacén. Devuelve el movimiento y el
// stock resultante del ledger de origen (movement.Origin indica cuál).
func (svc *Service) CreateWaste(ctx context.Context, tenantID string, in WasteInput, createdBy string) (WarehouseMovement, int, error) {
	if in.QuantityBase <= 0 {
		return WarehouseMovement{}, 0, ErrValidation
	}
	reason := strings.TrimSpace(in.Reason)
	if reason == "" {
		return WarehouseMovement{}, 0, ErrValidation
	}
	ok, err := svc.supplyOK(ctx, tenantID, strings.TrimSpace(in.SupplyID))
	if err != nil {
		return WarehouseMovement{}, 0, err
	}
	if !ok {
		return WarehouseMovement{}, 0, ErrInvalidSupply
	}
	createdAt := resolveCreatedAt(in.Date, svc.nowUTC())

	branchID := normalize(in.BranchID)
	if branchID == nil {
		// Merma en el almacén central.
		return svc.store.insertWasteWarehouse(ctx, tenantID, in.SupplyID, in.QuantityBase, reason, createdBy, createdAt)
	}
	// Merma en una sucursal: la sucursal debe ser del tenant.
	okBranch, err := svc.store.branchInTenant(ctx, tenantID, *branchID)
	if err != nil {
		return WarehouseMovement{}, 0, err
	}
	if !okBranch {
		return WarehouseMovement{}, 0, ErrInvalidBranch
	}
	return svc.store.insertWasteBranch(ctx, tenantID, in.SupplyID, *branchID, in.QuantityBase, reason, createdBy, createdAt)
}

func (svc *Service) ListWaste(ctx context.Context, tenantID string, from, to *time.Time) ([]WasteItem, error) {
	return svc.store.listWaste(ctx, tenantID, from, to)
}

// ---- Helpers ---------------------------------------------------------------

// supplyOK indica si el insumo pertenece al tenant (uuid mal formado/ajeno => false).
func (svc *Service) supplyOK(ctx context.Context, tenantID, supplyID string) (bool, error) {
	_, ok, err := svc.store.supplyInfo(ctx, tenantID, supplyID)
	return ok, err
}

// resolveCreatedAt mapea el campo Fecha del form a created_at (§4.4): vacío o fecha
// == hoy (o futuro) => nil (el store usa now(), preservando el orden del ledger);
// fecha pasada (backdating) => esa fecha a MEDIODÍA UTC. Fecha inválida => nil.
func resolveCreatedAt(date string, now time.Time) *time.Time {
	date = strings.TrimSpace(date)
	if date == "" {
		return nil
	}
	d, err := time.Parse("2006-01-02", date)
	if err != nil {
		return nil // defensivo: fecha ilegible => now()
	}
	n := now.UTC()
	today := time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, time.UTC)
	if !d.Before(today) {
		return nil // hoy o futuro => now()
	}
	noon := time.Date(d.Year(), d.Month(), d.Day(), 12, 0, 0, 0, time.UTC)
	return &noon
}

// normalize recorta un puntero de string; vacío => nil.
func normalize(s *string) *string {
	if s == nil {
		return nil
	}
	v := strings.TrimSpace(*s)
	if v == "" {
		return nil
	}
	return &v
}
