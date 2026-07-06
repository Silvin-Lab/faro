package customers

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrValidation = errors.New("validation")

type Service struct {
	store *store
}

func NewService(pool *pgxpool.Pool) *Service {
	return &Service{store: newStore(pool)}
}

func (svc *Service) Create(ctx context.Context, tenantID, phone, firstName, lastName string) (Customer, error) {
	phone = strings.TrimSpace(phone)
	firstName = strings.TrimSpace(firstName)
	lastName = strings.TrimSpace(lastName)
	if phone == "" || firstName == "" || lastName == "" {
		return Customer{}, ErrValidation
	}
	return svc.store.create(ctx, tenantID, phone, firstName, lastName)
}

func (svc *Service) FindByPhone(ctx context.Context, tenantID, phone string) (Customer, error) {
	phone = strings.TrimSpace(phone)
	if phone == "" {
		return Customer{}, ErrValidation
	}
	return svc.store.findByPhone(ctx, tenantID, phone)
}

// SetVisits fija las visitas del ciclo del cliente (ajuste manual: migración de
// tarjetas físicas). El acumulado de por vida nunca decrece (GREATEST en el store).
func (svc *Service) SetVisits(ctx context.Context, tenantID, id string, visits int) (Customer, error) {
	if visits < 0 {
		return Customer{}, ErrValidation
	}
	return svc.store.setVisits(ctx, tenantID, id, visits)
}

// Search busca clientes por nombre o teléfono (ILIKE). limit se acota a [1, 50].
func (svc *Service) Search(ctx context.Context, tenantID, q string, limit int) ([]Customer, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return nil, ErrValidation
	}
	if limit <= 0 {
		limit = 20
	}
	if limit > 50 {
		limit = 50
	}
	return svc.store.searchByQuery(ctx, tenantID, q, limit)
}
