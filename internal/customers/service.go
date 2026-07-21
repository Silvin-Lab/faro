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

// Create registra un cliente. priorVisits (opcional, ≥0) fija de una vez sus
// visitas del ciclo y de por vida en el mismo INSERT (tarjeta física migrada al
// registrar, ej. desde el POS) — evita el viaje de ida y vuelta de crear y luego
// ajustar visitas, que además está restringido a admin (SetVisits) y no debía
// bloquear a un cajero registrando un cliente nuevo.
func (svc *Service) Create(ctx context.Context, tenantID, phone, firstName, lastName string, priorVisits int) (Customer, error) {
	phone = strings.TrimSpace(phone)
	firstName = strings.TrimSpace(firstName)
	lastName = strings.TrimSpace(lastName)
	if phone == "" || firstName == "" || lastName == "" || priorVisits < 0 {
		return Customer{}, ErrValidation
	}
	return svc.store.create(ctx, tenantID, phone, firstName, lastName, priorVisits)
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

// List devuelve clientes paginados (sin filtro de texto), para el listado por
// default de la pantalla Clientes. limit se acota a [1, 50]; offset ≥ 0.
func (svc *Service) List(ctx context.Context, tenantID string, limit, offset int) ([]Customer, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 50 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	return svc.store.listAll(ctx, tenantID, limit, offset)
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
