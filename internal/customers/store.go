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

// create inserta el cliente con sus visitas iniciales (0 si no traía ninguna).
// visits y visits_lifetime arrancan iguales: de por vida nunca es menor al ciclo.
// createdBy es el usuario de la sesión (nunca del cliente). Si priorVisits > 0 se
// escribe una fila de auditoría ('create') en la MISMA transacción.
func (s *store) create(ctx context.Context, tenantID, phone, firstName, lastName string, priorVisits int, createdBy *string) (Customer, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Customer{}, err
	}
	defer tx.Rollback(ctx)

	var c Customer
	err = tx.QueryRow(ctx,
		`INSERT INTO customers (tenant_id, phone, first_name, last_name, visits, visits_lifetime, created_by)
		 VALUES ($1, $2, $3, $4, $5, $5, $6)
		 RETURNING id::text, tenant_id::text, phone, first_name, last_name, visits, visits_lifetime, created_at`,
		tenantID, phone, firstName, lastName, priorVisits, createdBy).
		Scan(&c.ID, &c.TenantID, &c.Phone, &c.FirstName, &c.LastName, &c.Visits, &c.VisitsLifetime, &c.CreatedAt)
	if dberr.IsUniqueViolation(err) {
		return Customer{}, ErrPhoneTaken
	}
	if err != nil {
		return Customer{}, err
	}

	// Auditoría: solo si se sembraron visitas al alta (el caso sensible). Un alta con
	// 0 visitas no registra cambio (no hay nada que auditar).
	if priorVisits > 0 {
		if _, err := tx.Exec(ctx,
			`INSERT INTO customer_visit_changes (tenant_id, customer_id, user_id, visits_before, visits_after, source)
			 VALUES ($1, $2, $3, 0, $4, 'create')`,
			tenantID, c.ID, createdBy, priorVisits); err != nil {
			return Customer{}, err
		}
	}

	c.CreatedByName, err = userName(ctx, tx, createdBy)
	if err != nil {
		return Customer{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Customer{}, err
	}
	return c, nil
}

// userName resuelve el nombre de un usuario por id (nil => nil).
func userName(ctx context.Context, q pgx.Tx, userID *string) (*string, error) {
	if userID == nil {
		return nil, nil
	}
	var name string
	err := q.QueryRow(ctx, `SELECT name FROM users WHERE id = $1`, *userID).Scan(&name)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &name, nil
}

func (s *store) findByPhone(ctx context.Context, tenantID, phone string) (Customer, error) {
	var c Customer
	err := s.pool.QueryRow(ctx,
		`SELECT c.id::text, c.tenant_id::text, c.phone, c.first_name, c.last_name, c.visits, c.visits_lifetime, u.name, c.created_at
		   FROM customers c LEFT JOIN users u ON u.id = c.created_by
		  WHERE c.tenant_id = $1 AND c.phone = $2`, tenantID, phone).
		Scan(&c.ID, &c.TenantID, &c.Phone, &c.FirstName, &c.LastName, &c.Visits, &c.VisitsLifetime, &c.CreatedByName, &c.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Customer{}, ErrNotFound
	}
	return c, err
}

// setVisits fija visits al valor dado y sube visits_lifetime con GREATEST (nunca
// baja). Acotado al negocio: si el cliente no existe o es de otro tenant -> ErrNotFound.
// userID es el usuario de la sesión; se escribe una fila de auditoría ('adjust')
// con el valor anterior y el nuevo en la MISMA transacción.
func (s *store) setVisits(ctx context.Context, tenantID, id string, visits int, userID *string) (Customer, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Customer{}, err
	}
	defer tx.Rollback(ctx)

	// Lock de la fila para leer el valor previo y actualizar de forma consistente.
	var before int
	err = tx.QueryRow(ctx,
		`SELECT visits FROM customers WHERE tenant_id = $1 AND id = $2 FOR UPDATE`,
		tenantID, id).Scan(&before)
	if errors.Is(err, pgx.ErrNoRows) || dberr.IsInvalidText(err) {
		return Customer{}, ErrNotFound
	}
	if err != nil {
		return Customer{}, err
	}

	var c Customer
	if err := tx.QueryRow(ctx,
		`UPDATE customers
		    SET visits = $3,
		        visits_lifetime = GREATEST(visits_lifetime, $3)
		  WHERE tenant_id = $1 AND id = $2
		 RETURNING id::text, tenant_id::text, phone, first_name, last_name, visits, visits_lifetime, created_at`,
		tenantID, id, visits).
		Scan(&c.ID, &c.TenantID, &c.Phone, &c.FirstName, &c.LastName, &c.Visits, &c.VisitsLifetime, &c.CreatedAt); err != nil {
		return Customer{}, err
	}

	if _, err := tx.Exec(ctx,
		`INSERT INTO customer_visit_changes (tenant_id, customer_id, user_id, visits_before, visits_after, source)
		 VALUES ($1, $2, $3, $4, $5, 'adjust')`,
		tenantID, c.ID, userID, before, visits); err != nil {
		return Customer{}, err
	}

	c.CreatedByName, err = createdByName(ctx, tx, tenantID, c.ID)
	if err != nil {
		return Customer{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Customer{}, err
	}
	return c, nil
}

// createdByName resuelve el nombre de quien registró al cliente (JOIN users).
func createdByName(ctx context.Context, q pgx.Tx, tenantID, customerID string) (*string, error) {
	var name *string
	err := q.QueryRow(ctx,
		`SELECT u.name FROM customers c LEFT JOIN users u ON u.id = c.created_by
		  WHERE c.id = $1 AND c.tenant_id = $2`, customerID, tenantID).Scan(&name)
	if err != nil {
		return nil, err
	}
	return name, nil
}

// visitChanges devuelve el historial de cambios de visitas de un cliente del
// negocio, más recientes primero, con el nombre de quién hizo cada cambio.
func (s *store) visitChanges(ctx context.Context, tenantID, customerID string) ([]VisitChange, error) {
	// Verificar que el cliente exista y sea del negocio (aislamiento + 404 limpio).
	var exists bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM customers WHERE id = $1 AND tenant_id = $2)`,
		customerID, tenantID).Scan(&exists)
	if dberr.IsInvalidText(err) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrNotFound
	}

	rows, err := s.pool.Query(ctx,
		`SELECT vc.id::text, vc.visits_before, vc.visits_after, vc.source, vc.user_id::text, u.name, vc.created_at
		   FROM customer_visit_changes vc
		   LEFT JOIN users u ON u.id = vc.user_id
		  WHERE vc.customer_id = $1 AND vc.tenant_id = $2
		  ORDER BY vc.created_at DESC`, customerID, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []VisitChange{}
	for rows.Next() {
		var v VisitChange
		if err := rows.Scan(&v.ID, &v.VisitsBefore, &v.VisitsAfter, &v.Source, &v.ByUserID, &v.ByName, &v.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// listAll devuelve clientes del negocio ordenados por nombre, paginados con
// limit/offset (listado por default de la pantalla Clientes, "mostrar más").
func (s *store) listAll(ctx context.Context, tenantID string, limit, offset int) ([]Customer, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT c.id::text, c.tenant_id::text, c.phone, c.first_name, c.last_name, c.visits, c.visits_lifetime, u.name, c.created_at
		   FROM customers c LEFT JOIN users u ON u.id = c.created_by
		  WHERE c.tenant_id = $1
		  ORDER BY c.first_name, c.last_name
		  LIMIT $2 OFFSET $3`, tenantID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Customer{}
	for rows.Next() {
		var c Customer
		if err := rows.Scan(&c.ID, &c.TenantID, &c.Phone, &c.FirstName, &c.LastName, &c.Visits, &c.VisitsLifetime, &c.CreatedByName, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// searchByQuery busca clientes por nombre (first/last) o teléfono con ILIKE
// '%q%', acotado al negocio y ordenado por nombre.
func (s *store) searchByQuery(ctx context.Context, tenantID, q string, limit int) ([]Customer, error) {
	pattern := "%" + q + "%"
	rows, err := s.pool.Query(ctx,
		`SELECT c.id::text, c.tenant_id::text, c.phone, c.first_name, c.last_name, c.visits, c.visits_lifetime, u.name, c.created_at
		   FROM customers c LEFT JOIN users u ON u.id = c.created_by
		  WHERE c.tenant_id = $1
		    AND (c.first_name ILIKE $2 OR c.last_name ILIKE $2 OR c.phone ILIKE $2)
		  ORDER BY c.first_name, c.last_name
		  LIMIT $3`, tenantID, pattern, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Customer{}
	for rows.Next() {
		var c Customer
		if err := rows.Scan(&c.ID, &c.TenantID, &c.Phone, &c.FirstName, &c.LastName, &c.Visits, &c.VisitsLifetime, &c.CreatedByName, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
