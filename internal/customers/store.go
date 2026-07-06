package customers

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"faro/internal/dberr"
)

var (
	ErrNotFound   = errors.New("not found")
	ErrPhoneTaken = errors.New("phone taken")
)

type store struct {
	pool *pgxpool.Pool
}

func newStore(pool *pgxpool.Pool) *store {
	return &store{pool: pool}
}

func (s *store) create(ctx context.Context, tenantID, phone, firstName, lastName string) (Customer, error) {
	var c Customer
	err := s.pool.QueryRow(ctx,
		`INSERT INTO customers (tenant_id, phone, first_name, last_name)
		 VALUES ($1, $2, $3, $4)
		 RETURNING id::text, tenant_id::text, phone, first_name, last_name, visits, visits_lifetime, created_at`,
		tenantID, phone, firstName, lastName).
		Scan(&c.ID, &c.TenantID, &c.Phone, &c.FirstName, &c.LastName, &c.Visits, &c.VisitsLifetime, &c.CreatedAt)
	if dberr.IsUniqueViolation(err) {
		return Customer{}, ErrPhoneTaken
	}
	return c, err
}

func (s *store) findByPhone(ctx context.Context, tenantID, phone string) (Customer, error) {
	var c Customer
	err := s.pool.QueryRow(ctx,
		`SELECT id::text, tenant_id::text, phone, first_name, last_name, visits, visits_lifetime, created_at
		   FROM customers WHERE tenant_id = $1 AND phone = $2`, tenantID, phone).
		Scan(&c.ID, &c.TenantID, &c.Phone, &c.FirstName, &c.LastName, &c.Visits, &c.VisitsLifetime, &c.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Customer{}, ErrNotFound
	}
	return c, err
}

// setVisits fija visits al valor dado y sube visits_lifetime con GREATEST (nunca
// baja). Acotado al negocio: si el cliente no existe o es de otro tenant -> ErrNotFound.
func (s *store) setVisits(ctx context.Context, tenantID, id string, visits int) (Customer, error) {
	var c Customer
	err := s.pool.QueryRow(ctx,
		`UPDATE customers
		    SET visits = $3,
		        visits_lifetime = GREATEST(visits_lifetime, $3)
		  WHERE tenant_id = $1 AND id = $2
		 RETURNING id::text, tenant_id::text, phone, first_name, last_name, visits, visits_lifetime, created_at`,
		tenantID, id, visits).
		Scan(&c.ID, &c.TenantID, &c.Phone, &c.FirstName, &c.LastName, &c.Visits, &c.VisitsLifetime, &c.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Customer{}, ErrNotFound
	}
	return c, err
}

// searchByQuery busca clientes por nombre (first/last) o teléfono con ILIKE
// '%q%', acotado al negocio y ordenado por nombre.
func (s *store) searchByQuery(ctx context.Context, tenantID, q string, limit int) ([]Customer, error) {
	pattern := "%" + q + "%"
	rows, err := s.pool.Query(ctx,
		`SELECT id::text, tenant_id::text, phone, first_name, last_name, visits, visits_lifetime, created_at
		   FROM customers
		  WHERE tenant_id = $1
		    AND (first_name ILIKE $2 OR last_name ILIKE $2 OR phone ILIKE $2)
		  ORDER BY first_name, last_name
		  LIMIT $3`, tenantID, pattern, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Customer{}
	for rows.Next() {
		var c Customer
		if err := rows.Scan(&c.ID, &c.TenantID, &c.Phone, &c.FirstName, &c.LastName, &c.Visits, &c.VisitsLifetime, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
