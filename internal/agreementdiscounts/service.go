package agreementdiscounts

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrValidation = errors.New("validation")

type Service struct {
	store *store
}

func NewService(pool *pgxpool.Pool) *Service {
	return &Service{store: newStore(pool)}
}

// List devuelve los descuentos del negocio. status: active (def.) | inactive | all.
func (svc *Service) List(ctx context.Context, tenantID, status string) ([]AgreementDiscount, error) {
	switch status {
	case "active", "inactive", "all":
	default:
		status = "active"
	}
	return svc.store.list(ctx, tenantID, status)
}

func (svc *Service) Get(ctx context.Context, tenantID, id string) (AgreementDiscount, error) {
	return svc.store.get(ctx, tenantID, id)
}

// Create valida el % (1..100) y crea el descuento activo.
func (svc *Service) Create(ctx context.Context, tenantID string, percent int) (AgreementDiscount, error) {
	if percent < 1 || percent > 100 {
		return AgreementDiscount{}, ErrValidation
	}
	return svc.store.create(ctx, tenantID, percent)
}

func (svc *Service) Update(ctx context.Context, tenantID, id string, percent int) (AgreementDiscount, error) {
	if percent < 1 || percent > 100 {
		return AgreementDiscount{}, ErrValidation
	}
	return svc.store.update(ctx, tenantID, id, percent)
}

func (svc *Service) Archive(ctx context.Context, tenantID, id string) error {
	return svc.store.archive(ctx, tenantID, id)
}
