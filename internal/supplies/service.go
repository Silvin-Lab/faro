package supplies

import (
	"context"
	"errors"
	"math"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrValidation indica datos inválidos (nombre/presentación vacíos, base_unit
// ilegal o inmutable, cantidades fuera de rango, motivo faltante en un ajuste).
var ErrValidation = errors.New("validation")

// baseUnits son las unidades base permitidas (deben coincidir con el CHECK de 0015).
var baseUnits = map[string]bool{"g": true, "ml": true, "pieza": true}

// Service expone la lógica de insumos: catálogo, inventario por sucursal y recetas.
type Service struct {
	store *store
}

func NewService(pool *pgxpool.Pool) *Service {
	return &Service{store: newStore(pool)}
}

// ---- Categorías de insumo --------------------------------------------------

func (svc *Service) CreateCategory(ctx context.Context, tenantID, name string, sortOrder int) (SupplyCategory, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return SupplyCategory{}, ErrValidation
	}
	return svc.store.createCategory(ctx, tenantID, name, sortOrder)
}

func (svc *Service) ListCategories(ctx context.Context, tenantID string) ([]SupplyCategory, error) {
	return svc.store.listCategories(ctx, tenantID)
}

// CategoryUpdate agrupa los cambios parciales de una categoría (nil = no cambia).
type CategoryUpdate struct {
	Name      *string
	Status    *string
	SortOrder *int
}

func (svc *Service) UpdateCategory(ctx context.Context, tenantID, id string, in CategoryUpdate) (SupplyCategory, error) {
	if in.Name != nil {
		n := strings.TrimSpace(*in.Name)
		if n == "" {
			return SupplyCategory{}, ErrValidation
		}
		in.Name = &n
	}
	if in.Status != nil && *in.Status != "active" && *in.Status != "inactive" {
		return SupplyCategory{}, ErrValidation
	}
	return svc.store.updateCategory(ctx, tenantID, id, in.Name, in.Status, in.SortOrder)
}

// ---- Catálogo --------------------------------------------------------------

// Create registra un insumo. packageCostCents es OPCIONAL (nil = costo no
// capturado); si viene, debe ser >= 0 (validation_error). categoryID es OPCIONAL
// (nil / "" = sin categoría); si viene, debe ser una categoría del tenant
// (invalid_category).
func (svc *Service) Create(ctx context.Context, tenantID, name, baseUnit, packageName string, packageContent int, packageCostCents *int, categoryID *string) (Supply, error) {
	name = strings.TrimSpace(name)
	packageName = strings.TrimSpace(packageName)
	if name == "" || packageName == "" {
		return Supply{}, ErrValidation
	}
	if !baseUnits[baseUnit] {
		return Supply{}, ErrValidation
	}
	if packageContent <= 0 {
		return Supply{}, ErrValidation
	}
	if packageCostCents != nil && *packageCostCents < 0 {
		return Supply{}, ErrValidation
	}
	categoryID = normalizeID(categoryID)
	if categoryID != nil {
		ok, err := svc.store.categoryExists(ctx, tenantID, *categoryID)
		if err != nil {
			return Supply{}, err
		}
		if !ok {
			return Supply{}, ErrInvalidCategory
		}
	}
	return svc.store.create(ctx, tenantID, name, baseUnit, packageName, packageContent, packageCostCents, categoryID)
}

func (svc *Service) List(ctx context.Context, tenantID string) ([]Supply, error) {
	return svc.store.list(ctx, tenantID)
}

func (svc *Service) Get(ctx context.Context, tenantID, id string) (Supply, error) {
	return svc.store.get(ctx, tenantID, id)
}

// UpdateInput agrupa los cambios parciales (nil = no cambia). BaseUnit se incluye
// solo para rechazar el intento de cambiarla (inmutable): si viene => ErrValidation.
type UpdateInput struct {
	Name             *string
	Status           *string
	PackageName      *string
	PackageContent   *int
	PackageCostCents *int    // nil = no cambia; si viene, >= 0
	BaseUnit         *string // presente en el PATCH => intento ilegal de mutar la unidad
	CategoryID       *string // presente => asigna esa categoría (validada); ausente = no cambia
}

func (svc *Service) Update(ctx context.Context, tenantID, id string, in UpdateInput) (Supply, error) {
	// base_unit es INMUTABLE: cualquier intento de enviarla en el PATCH es un error
	// de validación (no se compara con el valor actual; simplemente no se acepta).
	if in.BaseUnit != nil {
		return Supply{}, ErrValidation
	}
	if in.Name != nil {
		n := strings.TrimSpace(*in.Name)
		if n == "" {
			return Supply{}, ErrValidation
		}
		in.Name = &n
	}
	if in.PackageName != nil {
		p := strings.TrimSpace(*in.PackageName)
		if p == "" {
			return Supply{}, ErrValidation
		}
		in.PackageName = &p
	}
	if in.PackageContent != nil && *in.PackageContent <= 0 {
		return Supply{}, ErrValidation
	}
	if in.PackageCostCents != nil && *in.PackageCostCents < 0 {
		return Supply{}, ErrValidation
	}
	if in.Status != nil && *in.Status != "active" && *in.Status != "inactive" {
		return Supply{}, ErrValidation
	}
	// Categoría: presente => asigna (validando tenant); ausente/"" => no cambia. NO se
	// puede desasignar a null vía PATCH (se reasigna a otra); ver decisión en el plan.
	catID := normalizeID(in.CategoryID)
	setCategory := catID != nil
	if setCategory {
		ok, err := svc.store.categoryExists(ctx, tenantID, *catID)
		if err != nil {
			return Supply{}, err
		}
		if !ok {
			return Supply{}, ErrInvalidCategory
		}
	}
	return svc.store.update(ctx, tenantID, id, in.Name, in.Status, in.PackageName, in.PackageContent, in.PackageCostCents, catID, setCategory)
}

// ---- Movimientos -----------------------------------------------------------

// MovementInput es la petición de un movimiento manual (purchase o adjustment).
// Packages/QuantityBase son punteros para distinguir "ausente" de "cero".
type MovementInput struct {
	Type         string
	BranchID     string
	Packages     *int
	QuantityBase *int
	Reason       *string
}

// CreateMovement valida y aplica un movimiento manual. Devuelve el movimiento y el
// nuevo stock de la sucursal. NADA es bloqueante: jamás valida existencias (el
// stock puede quedar negativo). El type 'sale' NO se acepta por esta vía (lo
// escribe el descuento automático de la venta en otra fase).
func (svc *Service) CreateMovement(ctx context.Context, tenantID, supplyID string, in MovementInput, createdBy string) (Movement, int, error) {
	// El insumo debe ser del tenant.
	if _, err := svc.store.get(ctx, tenantID, supplyID); err != nil {
		return Movement{}, 0, err // ErrNotFound o error real
	}

	// branchId obligatorio y del tenant.
	branchID := strings.TrimSpace(in.BranchID)
	if branchID == "" {
		return Movement{}, 0, ErrValidation
	}
	ok, err := svc.store.branchInTenant(ctx, tenantID, branchID)
	if err != nil {
		return Movement{}, 0, err
	}
	if !ok {
		return Movement{}, 0, ErrInvalidBranch
	}

	var delta int
	var reason *string
	switch in.Type {
	case "purchase":
		// Exactamente uno de packages>0 o quantityBase>0.
		hasPackages := in.Packages != nil && *in.Packages > 0
		hasQty := in.QuantityBase != nil && *in.QuantityBase > 0
		if hasPackages == hasQty { // ninguno o ambos => inválido
			return Movement{}, 0, ErrValidation
		}
		if hasPackages {
			sp, err := svc.store.get(ctx, tenantID, supplyID)
			if err != nil {
				return Movement{}, 0, err
			}
			delta = *in.Packages * sp.PackageContent
		} else {
			delta = *in.QuantityBase
		}
		reason = normalizeReason(in.Reason)
	case "adjustment":
		// Cantidad firmada distinta de cero + motivo obligatorio.
		if in.QuantityBase == nil || *in.QuantityBase == 0 {
			return Movement{}, 0, ErrValidation
		}
		r := normalizeReason(in.Reason)
		if r == nil {
			return Movement{}, 0, ErrValidation
		}
		delta = *in.QuantityBase
		reason = r
	default:
		// Incluye 'sale' y cualquier otro valor: no permitido por esta vía.
		return Movement{}, 0, ErrValidation
	}

	return svc.store.insertMovement(ctx, tenantID, supplyID, branchID, in.Type, delta, reason, createdBy)
}

func (svc *Service) ListMovements(ctx context.Context, tenantID, supplyID string, from, to *time.Time) ([]Movement, error) {
	// El insumo debe ser del tenant (aísla y da 404 si es ajeno).
	if _, err := svc.store.get(ctx, tenantID, supplyID); err != nil {
		return nil, err
	}
	return svc.store.listMovements(ctx, tenantID, supplyID, from, to)
}

// ---- Medidas de uso --------------------------------------------------------

// ListMeasures devuelve las medidas de un insumo del tenant (404 si el insumo es
// ajeno/inexistente).
func (svc *Service) ListMeasures(ctx context.Context, tenantID, supplyID string) ([]SupplyMeasure, error) {
	if _, err := svc.store.get(ctx, tenantID, supplyID); err != nil {
		return nil, err // ErrNotFound o error real
	}
	return svc.store.listMeasures(ctx, tenantID, supplyID)
}

// CreateMeasure crea una medida para un insumo del tenant. Nombre requerido y
// baseQuantity > 0 (ErrValidation); insumo ajeno/inexistente => ErrNotFound; nombre
// duplicado por insumo => ErrNameTaken.
func (svc *Service) CreateMeasure(ctx context.Context, tenantID, supplyID, name string, baseQuantity int) (SupplyMeasure, error) {
	if _, err := svc.store.get(ctx, tenantID, supplyID); err != nil {
		return SupplyMeasure{}, err
	}
	name = strings.TrimSpace(name)
	if name == "" || baseQuantity <= 0 {
		return SupplyMeasure{}, ErrValidation
	}
	return svc.store.createMeasure(ctx, tenantID, supplyID, name, baseQuantity)
}

// MeasureUpdate agrupa los cambios parciales de una medida (nil = no cambia).
type MeasureUpdate struct {
	Name         *string
	BaseQuantity *int
}

// UpdateMeasure aplica cambios parciales a una medida del tenant. Si baseQuantity
// cambia, el store recomputa el quantity_base de las recetas que la usan en la misma
// transacción (recálculo en vivo). name vacío / baseQuantity <= 0 => ErrValidation;
// medida ajena/inexistente => ErrNotFound; nombre duplicado => ErrNameTaken.
func (svc *Service) UpdateMeasure(ctx context.Context, tenantID, measureID string, in MeasureUpdate) (SupplyMeasure, error) {
	if in.Name != nil {
		n := strings.TrimSpace(*in.Name)
		if n == "" {
			return SupplyMeasure{}, ErrValidation
		}
		in.Name = &n
	}
	if in.BaseQuantity != nil && *in.BaseQuantity <= 0 {
		return SupplyMeasure{}, ErrValidation
	}
	return svc.store.updateMeasure(ctx, tenantID, measureID, in.Name, in.BaseQuantity)
}

// DeleteMeasure borra una medida del tenant. Las recetas que la usaban conservan su
// quantity_base "congelado" (ON DELETE SET NULL). Medida ajena/inexistente =>
// ErrNotFound.
func (svc *Service) DeleteMeasure(ctx context.Context, tenantID, measureID string) error {
	return svc.store.deleteMeasure(ctx, tenantID, measureID)
}

// ---- Recetas ---------------------------------------------------------------

func (svc *Service) GetRecipe(ctx context.Context, tenantID, productID string) ([]RecipeItem, error) {
	return svc.store.getRecipe(ctx, tenantID, productID)
}

// ReplaceRecipe reemplaza la receta completa del producto. Valida producto del
// tenant (ErrNotFound) e insumos del tenant + quantity_base > 0 (ErrInvalidSupply
// / ErrValidation). Reemplazo idempotente: la lista enviada es el estado final.
func (svc *Service) ReplaceRecipe(ctx context.Context, tenantID, productID string, items []recipeItemIn) ([]RecipeItem, error) {
	ok, err := svc.store.productInTenant(ctx, tenantID, productID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrNotFound
	}
	seen := map[string]bool{}
	for i := range items {
		it := &items[i]
		if strings.TrimSpace(it.SupplyID) == "" {
			return nil, ErrValidation
		}
		if seen[it.SupplyID] {
			return nil, ErrValidation // insumo repetido en la misma receta
		}
		seen[it.SupplyID] = true

		measureID := normalizeID(it.MeasureID)
		if measureID != nil {
			// Línea por medida: la medida debe pertenecer a ESE insumo y al tenant.
			// quantity_base = ROUND(count * base); si redondea a < 1 viola el CHECK y
			// no tiene sentido físico -> validation_error.
			if it.MeasureCount == nil || *it.MeasureCount <= 0 {
				return nil, ErrValidation
			}
			base, ok, err := svc.store.measureForSupply(ctx, tenantID, it.SupplyID, *measureID)
			if err != nil {
				return nil, err
			}
			if !ok {
				return nil, ErrInvalidMeasure
			}
			qb := int(math.Round(*it.MeasureCount * float64(base)))
			if qb < 1 {
				return nil, ErrValidation
			}
			it.MeasureID = measureID
			it.QuantityBase = qb
		} else {
			// Línea en unidad base: quantity_base directo (comportamiento actual).
			it.MeasureID = nil
			it.MeasureCount = nil
			if it.QuantityBase <= 0 {
				return nil, ErrValidation
			}
		}
	}
	return svc.store.replaceRecipe(ctx, tenantID, productID, items)
}

// normalizeID convierte cadenas vacías/espacios en nil (categoría opcional).
func normalizeID(u *string) *string {
	if u == nil || strings.TrimSpace(*u) == "" {
		return nil
	}
	v := strings.TrimSpace(*u)
	return &v
}

// normalizeReason recorta el motivo; cadena vacía => nil.
func normalizeReason(r *string) *string {
	if r == nil {
		return nil
	}
	v := strings.TrimSpace(*r)
	if v == "" {
		return nil
	}
	return &v
}
