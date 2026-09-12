package products

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrValidation         = errors.New("validation")
	ErrInvalidCategory    = errors.New("invalid category")
	ErrFulfillmentBlocked = errors.New("fulfillment change blocked")
)

// validFulfillmentType indica si el tipo de surtido es soportado (M10).
func validFulfillmentType(t string) bool {
	return t == "branch_prepared" || t == "bakery"
}

// FulfillmentBlockedError es el error tipado de un cambio bakery -> branch_prepared
// bloqueado (§3.3 D-C): lleva los conteos de bloqueadores para el 409. Envuelve
// ErrFulfillmentBlocked (errors.Is) y se extrae con errors.As. Thread-safe (no hay
// estado compartido en el Service).
type FulfillmentBlockedError struct {
	OpenOrders        int
	BranchesWithStock int
}

func (e *FulfillmentBlockedError) Error() string { return "fulfillment change blocked" }
func (e *FulfillmentBlockedError) Unwrap() error  { return ErrFulfillmentBlocked }

type Service struct {
	store *store
}

func NewService(pool *pgxpool.Pool) *Service {
	return &Service{store: newStore(pool)}
}

// normalizeCategory devuelve la categoría validada (o nil) para el negocio.
func (svc *Service) normalizeCategory(ctx context.Context, tenantID string, categoryID *string) (*string, error) {
	if categoryID == nil || strings.TrimSpace(*categoryID) == "" {
		return nil, nil
	}
	id := strings.TrimSpace(*categoryID)
	ok, err := svc.store.categoryBelongsToTenant(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrInvalidCategory
	}
	return &id, nil
}

type CreateInput struct {
	Name            string
	PriceCents      int
	CategoryID      *string
	ImageURL        *string
	FulfillmentType *string // nil => default branch_prepared
}

func (svc *Service) Create(ctx context.Context, tenantID string, in CreateInput) (Product, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" || in.PriceCents <= 0 {
		return Product{}, ErrValidation
	}
	ft := "branch_prepared"
	if in.FulfillmentType != nil {
		ft = strings.TrimSpace(*in.FulfillmentType)
		if !validFulfillmentType(ft) {
			return Product{}, ErrValidation
		}
	}
	cat, err := svc.normalizeCategory(ctx, tenantID, in.CategoryID)
	if err != nil {
		return Product{}, err
	}
	return svc.store.create(ctx, tenantID, cat, name, in.PriceCents, normalizeURL(in.ImageURL), ft)
}

// normalizeURL convierte cadenas vacías en nil.
func normalizeURL(u *string) *string {
	if u == nil || strings.TrimSpace(*u) == "" {
		return nil
	}
	v := strings.TrimSpace(*u)
	return &v
}

func (svc *Service) List(ctx context.Context, tenantID string) ([]Product, error) {
	return svc.store.listByTenant(ctx, tenantID)
}

func (svc *Service) Get(ctx context.Context, tenantID, id string) (Product, error) {
	return svc.store.get(ctx, tenantID, id)
}

type UpdateInput struct {
	Name            *string
	PriceCents      *int
	CategoryID      *string // nil = no cambia; valor = asigna (validado por negocio)
	Status          *string
	ImageURL        *string // nil = no cambia; valor = asigna
	FulfillmentType *string // nil = no cambia; valor = cambia (sujeto a la regla D-C)
}

func (svc *Service) Update(ctx context.Context, tenantID, id string, in UpdateInput) (Product, error) {
	if in.Name != nil {
		n := strings.TrimSpace(*in.Name)
		if n == "" {
			return Product{}, ErrValidation
		}
		in.Name = &n
	}
	if in.PriceCents != nil && *in.PriceCents <= 0 {
		return Product{}, ErrValidation
	}
	if in.Status != nil && *in.Status != "active" && *in.Status != "inactive" {
		return Product{}, ErrValidation
	}
	if in.CategoryID != nil {
		cat, err := svc.normalizeCategory(ctx, tenantID, in.CategoryID)
		if err != nil {
			return Product{}, err
		}
		in.CategoryID = cat // categoría validada (o nil si venía vacía)
	}
	if in.FulfillmentType != nil {
		ft := strings.TrimSpace(*in.FulfillmentType)
		if !validFulfillmentType(ft) {
			return Product{}, ErrValidation
		}
		in.FulfillmentType = &ft
		// Regla D-C (§3.3): validar el cambio contra el estado actual bajo el mismo
		// criterio cross-módulo que sales->supplies.
		current, err := svc.store.currentFulfillmentType(ctx, tenantID, id)
		if err != nil {
			return Product{}, err
		}
		if current == "bakery" && ft == "branch_prepared" {
			// bakery -> branch_prepared: bloqueado si hay pedidos abiertos o stock de postre
			// distinto de 0 (evita pedidos inproducibles y doble contabilidad; ADR-010 R7).
			openOrders, branchesWithStock, err := svc.store.fulfillmentChangeBlockers(ctx, tenantID, id)
			if err != nil {
				return Product{}, err
			}
			if openOrders > 0 || branchesWithStock > 0 {
				return Product{}, &FulfillmentBlockedError{OpenOrders: openOrders, BranchesWithStock: branchesWithStock}
			}
		}
		// branch_prepared -> bakery: permitido siempre (no pudo tener pedidos/stock).
		// bakery -> bakery / branch_prepared -> branch_prepared: sin efecto.
	}
	return svc.store.update(ctx, tenantID, id, in.Name, in.PriceCents, in.CategoryID, in.Status, normalizeURL(in.ImageURL), in.FulfillmentType)
}
