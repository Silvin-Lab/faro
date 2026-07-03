package loyalty

import (
	"context"
	"errors"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrValidation = errors.New("validation")

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

type Service struct {
	store *store
}

func NewService(pool *pgxpool.Pool) *Service {
	return &Service{store: newStore(pool)}
}

// List devuelve las promociones del negocio. status: active (def.) | inactive | all.
func (svc *Service) List(ctx context.Context, tenantID, status string) ([]Promotion, error) {
	switch status {
	case "active", "inactive", "all":
	default:
		status = "active"
	}
	return svc.store.list(ctx, tenantID, status)
}

func (svc *Service) Get(ctx context.Context, tenantID, id string) (Promotion, error) {
	return svc.store.get(ctx, tenantID, id)
}

func (svc *Service) Create(ctx context.Context, tenantID string, in PromotionInput) (Promotion, error) {
	in, err := normalize(in)
	if err != nil {
		return Promotion{}, err
	}
	return svc.store.create(ctx, tenantID, in)
}

func (svc *Service) Update(ctx context.Context, tenantID, id string, in PromotionInput) (Promotion, error) {
	in, err := normalize(in)
	if err != nil {
		return Promotion{}, err
	}
	return svc.store.update(ctx, tenantID, id, in)
}

func (svc *Service) Archive(ctx context.Context, tenantID, id string) error {
	return svc.store.archive(ctx, tenantID, id)
}

func (svc *Service) CustomerStatus(ctx context.Context, tenantID, customerID string) (CustomerStatus, error) {
	return svc.store.customerStatus(ctx, tenantID, customerID)
}

// normalize valida y limpia la entrada de una promoción: nombre no vacío,
// discountPercent 1..100, visitThreshold > 0, y al menos un product_id válido.
func normalize(in PromotionInput) (PromotionInput, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return PromotionInput{}, ErrValidation
	}
	if in.DiscountPercent < 1 || in.DiscountPercent > 100 {
		return PromotionInput{}, ErrValidation
	}
	if in.VisitThreshold <= 0 {
		return PromotionInput{}, ErrValidation
	}
	in.ProductIDs = filterUUIDs(in.ProductIDs)
	if len(in.ProductIDs) == 0 {
		return PromotionInput{}, ErrValidation
	}
	return in, nil
}

func filterUUIDs(ids []string) []string {
	out := []string{}
	for _, id := range ids {
		if uuidRe.MatchString(strings.TrimSpace(id)) {
			out = append(out, strings.TrimSpace(id))
		}
	}
	return out
}
