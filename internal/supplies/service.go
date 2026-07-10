package supplies

import (
	"context"
	"errors"
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

// ---- Catálogo --------------------------------------------------------------

// Create registra un insumo. packageCostCents es OPCIONAL (nil = costo no
// capturado); si viene, debe ser >= 0 (validation_error).
func (svc *Service) Create(ctx context.Context, tenantID, name, baseUnit, packageName string, packageContent int, packageCostCents *int) (Supply, error) {
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
	return svc.store.create(ctx, tenantID, name, baseUnit, packageName, packageContent, packageCostCents)
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
	return svc.store.update(ctx, tenantID, id, in.Name, in.Status, in.PackageName, in.PackageContent, in.PackageCostCents)
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
	for _, it := range items {
		if strings.TrimSpace(it.SupplyID) == "" || it.QuantityBase <= 0 {
			return nil, ErrValidation
		}
		if seen[it.SupplyID] {
			return nil, ErrValidation // insumo repetido en la misma receta
		}
		seen[it.SupplyID] = true
	}
	return svc.store.replaceRecipe(ctx, tenantID, productID, items)
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
