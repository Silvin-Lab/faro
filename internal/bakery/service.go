package bakery

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Service orquesta la lógica de repostería: pedidos, producción (doble efecto de stock),
// stock de postre y auditoría. Tenant-scoped; el gating por rol lo aplican los handlers
// (inline). Todas las mutaciones de stock viven en transacciones del store.
type Service struct {
	store *store
}

func NewService(pool *pgxpool.Pool) *Service {
	return &Service{store: newStore(pool)}
}

// CreateOrder valida (cantidad > 0, sucursal del negocio, producto activo de repostería) y
// crea un pedido en 'pending'. branchID = sucursal activa del solicitante (no del cliente).
func (svc *Service) CreateOrder(ctx context.Context, tenantID, branchID, productID string, quantity int, note *string, requestedBy string) (Order, error) {
	branchID = strings.TrimSpace(branchID)
	productID = strings.TrimSpace(productID)
	if quantity <= 0 {
		return Order{}, ErrValidation
	}
	if branchID == "" {
		return Order{}, ErrInvalidBranch
	}
	okBranch, err := svc.store.branchInTenant(ctx, tenantID, branchID)
	if err != nil {
		return Order{}, err
	}
	if !okBranch {
		return Order{}, ErrInvalidBranch
	}
	okProduct, err := svc.store.productBakeryActive(ctx, tenantID, productID)
	if err != nil {
		return Order{}, err
	}
	if !okProduct {
		return Order{}, ErrInvalidProduct
	}
	return svc.store.createOrder(ctx, tenantID, branchID, productID, quantity, normalizeNote(note), requestedBy)
}

func (svc *Service) ListOrders(ctx context.Context, tenantID string, branchID *string, statuses []string, q string) ([]Order, error) {
	return svc.store.listOrders(ctx, tenantID, branchID, statuses, strings.TrimSpace(q))
}

func (svc *Service) GetOrder(ctx context.Context, tenantID, id string) (Order, error) {
	return svc.store.getOrder(ctx, tenantID, id)
}

func (svc *Service) ProductionsForOrder(ctx context.Context, tenantID, orderID string) ([]Production, error) {
	return svc.store.productionsForOrder(ctx, tenantID, orderID)
}

// Produce valida cantidad > 0 y ejecuta la transacción de producción (§4).
func (svc *Service) Produce(ctx context.Context, tenantID, orderID string, quantity int, createdBy string) (Order, Production, error) {
	if quantity <= 0 {
		return Order{}, Production{}, ErrValidation
	}
	return svc.store.produce(ctx, tenantID, orderID, quantity, createdBy)
}

func (svc *Service) CancelOrder(ctx context.Context, tenantID, id string) (Order, error) {
	return svc.store.cancelOrder(ctx, tenantID, id)
}

func (svc *Service) ReceiveOrder(ctx context.Context, tenantID, id string) (Order, error) {
	return svc.store.receiveOrder(ctx, tenantID, id)
}

func (svc *Service) ListStock(ctx context.Context, tenantID string, branchID *string, q string) ([]StockItem, error) {
	return svc.store.listStock(ctx, tenantID, branchID, strings.TrimSpace(q))
}

func (svc *Service) ListProductions(ctx context.Context, tenantID string, from, to *time.Time, branchID, productID *string) ([]ProductionAudit, error) {
	return svc.store.listProductions(ctx, tenantID, from, to, branchID, productID)
}

// ---- Merma de postre -------------------------------------------------------

// CreateWaste valida (cantidad > 0, motivo requerido, sucursal del negocio) y registra
// una merma de postre. El producto se revalida bakery+activo dentro de la transacción del
// store. branchID = sucursal efectiva resuelta por el handler (activa o del body si
// super_admin). quantity se guarda como negativo.
func (svc *Service) CreateWaste(ctx context.Context, tenantID, branchID, productID string, quantity int, reason string, createdBy string) (WasteMovement, error) {
	branchID = strings.TrimSpace(branchID)
	productID = strings.TrimSpace(productID)
	reason = strings.TrimSpace(reason)
	if quantity <= 0 || reason == "" {
		return WasteMovement{}, ErrValidation
	}
	if branchID == "" {
		return WasteMovement{}, ErrInvalidBranch
	}
	okBranch, err := svc.store.branchInTenant(ctx, tenantID, branchID)
	if err != nil {
		return WasteMovement{}, err
	}
	if !okBranch {
		return WasteMovement{}, ErrInvalidBranch
	}
	return svc.store.insertWaste(ctx, tenantID, productID, branchID, quantity, reason, createdBy)
}

func (svc *Service) ListWaste(ctx context.Context, tenantID string, from, to *time.Time, branchID *string) ([]WasteAudit, error) {
	return svc.store.listWaste(ctx, tenantID, from, to, branchID)
}

// ---- Conteo de cierre de postres -------------------------------------------

// countLine es una línea de conteo ya validada (con nombre resuelto), lista para el store.
type countLine struct {
	ProductID   string
	ProductName string
	CountedQty  int
}

// CreateCount valida (sucursal del negocio, al menos una línea, contado >= 0, productos
// bakery+activos sin duplicados) y ejecuta la reconciliación del conteo. Ordena las líneas
// por product_id ASC (orden de bloqueo determinista, anti-deadlock). branchID = sucursal
// efectiva resuelta por el handler.
func (svc *Service) CreateCount(ctx context.Context, tenantID, branchID string, note *string, items []CountLineInput, createdBy string) (CountDetail, error) {
	branchID = strings.TrimSpace(branchID)
	if branchID == "" {
		return CountDetail{}, ErrInvalidBranch
	}
	if len(items) == 0 {
		return CountDetail{}, ErrValidation
	}
	okBranch, err := svc.store.branchInTenant(ctx, tenantID, branchID)
	if err != nil {
		return CountDetail{}, err
	}
	if !okBranch {
		return CountDetail{}, ErrInvalidBranch
	}

	seen := map[string]bool{}
	lines := make([]countLine, 0, len(items))
	for _, it := range items {
		pid := strings.TrimSpace(it.ProductID)
		if pid == "" || it.CountedQty < 0 {
			return CountDetail{}, ErrValidation
		}
		if seen[pid] {
			return CountDetail{}, ErrValidation
		}
		seen[pid] = true
		name, err := svc.store.productBakeryName(ctx, tenantID, pid)
		if err != nil {
			return CountDetail{}, err
		}
		if name == "" {
			return CountDetail{}, ErrInvalidProduct
		}
		lines = append(lines, countLine{ProductID: pid, ProductName: name, CountedQty: it.CountedQty})
	}
	// Orden de bloqueo determinista por product_id (anti-deadlock, mismo criterio que produce()).
	sort.Slice(lines, func(i, j int) bool { return lines[i].ProductID < lines[j].ProductID })

	return svc.store.createCount(ctx, tenantID, branchID, normalizeNote(note), lines, createdBy)
}

func (svc *Service) GetCount(ctx context.Context, tenantID, id string) (CountDetail, error) {
	return svc.store.getCount(ctx, tenantID, id)
}

func (svc *Service) ListCounts(ctx context.Context, tenantID string, branchID *string, from, to *time.Time) ([]Count, error) {
	return svc.store.listCounts(ctx, tenantID, branchID, from, to)
}

// normalizeNote recorta la nota; vacía => nil.
func normalizeNote(note *string) *string {
	if note == nil {
		return nil
	}
	v := strings.TrimSpace(*note)
	if v == "" {
		return nil
	}
	return &v
}
