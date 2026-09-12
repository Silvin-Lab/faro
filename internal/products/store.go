package products

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"faro/internal/dberr"
)

var (
	ErrNotFound  = errors.New("not found")
	ErrNameTaken = errors.New("name taken")
)

type store struct {
	pool *pgxpool.Pool
}

func newStore(pool *pgxpool.Pool) *store {
	return &store{pool: pool}
}

// categoryBelongsToTenant valida que la categoría sea del mismo negocio.
func (s *store) categoryBelongsToTenant(ctx context.Context, tenantID, categoryID string) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM categories WHERE id = $1 AND tenant_id = $2)`,
		categoryID, tenantID).Scan(&ok)
	if dberr.IsInvalidText(err) { // uuid mal formado -> tratamos como inexistente
		return false, nil
	}
	return ok, err
}

func (s *store) create(ctx context.Context, tenantID string, categoryID *string, name string, priceCents int, imageURL *string, fulfillmentType string) (Product, error) {
	var p Product
	err := s.pool.QueryRow(ctx,
		`INSERT INTO products (tenant_id, category_id, name, price_cents, image_url, fulfillment_type)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 RETURNING id::text, tenant_id::text, category_id::text, name, price_cents, status, image_url, fulfillment_type, created_at`,
		tenantID, categoryID, name, priceCents, imageURL, fulfillmentType).
		Scan(&p.ID, &p.TenantID, &p.CategoryID, &p.Name, &p.PriceCents, &p.Status, &p.ImageURL, &p.FulfillmentType, &p.CreatedAt)
	if dberr.IsUniqueViolation(err) {
		return Product{}, ErrNameTaken
	}
	return p, err
}

func (s *store) listByTenant(ctx context.Context, tenantID string) ([]Product, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT p.id::text, p.tenant_id::text, p.category_id::text, c.name,
		        p.name, p.price_cents, p.status, p.image_url, p.fulfillment_type, p.created_at
		   FROM products p
		   LEFT JOIN categories c ON c.id = p.category_id
		  WHERE p.tenant_id = $1
		  ORDER BY p.name`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Product
	for rows.Next() {
		var p Product
		if err := rows.Scan(&p.ID, &p.TenantID, &p.CategoryID, &p.CategoryName,
			&p.Name, &p.PriceCents, &p.Status, &p.ImageURL, &p.FulfillmentType, &p.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *store) get(ctx context.Context, tenantID, id string) (Product, error) {
	var p Product
	err := s.pool.QueryRow(ctx,
		`SELECT p.id::text, p.tenant_id::text, p.category_id::text, c.name,
		        p.name, p.price_cents, p.status, p.image_url, p.fulfillment_type, p.created_at
		   FROM products p
		   LEFT JOIN categories c ON c.id = p.category_id
		  WHERE p.id = $1 AND p.tenant_id = $2`, id, tenantID).
		Scan(&p.ID, &p.TenantID, &p.CategoryID, &p.CategoryName, &p.Name, &p.PriceCents, &p.Status, &p.ImageURL, &p.FulfillmentType, &p.CreatedAt)
	switch {
	case errors.Is(err, pgx.ErrNoRows), dberr.IsInvalidText(err):
		return Product{}, ErrNotFound
	case err != nil:
		return Product{}, err
	}
	return p, nil
}

// currentFulfillmentType devuelve el fulfillment_type actual del producto del negocio.
// Se usa para aplicar la regla D-C de cambio de tipo. uuid mal formado/ajeno => ErrNotFound.
func (s *store) currentFulfillmentType(ctx context.Context, tenantID, id string) (string, error) {
	var ft string
	err := s.pool.QueryRow(ctx,
		`SELECT fulfillment_type FROM products WHERE id = $1 AND tenant_id = $2`, id, tenantID).Scan(&ft)
	switch {
	case errors.Is(err, pgx.ErrNoRows), dberr.IsInvalidText(err):
		return "", ErrNotFound
	case err != nil:
		return "", err
	}
	return ft, nil
}

// fulfillmentChangeBlockers cuenta los bloqueadores de un cambio bakery -> branch_prepared
// (regla D-C, §3.3): pedidos ABIERTOS (pending/in_production) y sucursales con stock de
// postre distinto de 0. Consulta directa contra las tablas de bakery (mismo criterio
// cross-módulo que sales->supplies). Cero en ambos => el cambio es seguro.
func (s *store) fulfillmentChangeBlockers(ctx context.Context, tenantID, productID string) (openOrders, branchesWithStock int, err error) {
	if e := s.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM bakery_orders
		  WHERE tenant_id = $1 AND product_id = $2 AND status IN ('pending','in_production')`,
		tenantID, productID).Scan(&openOrders); e != nil {
		return 0, 0, e
	}
	if e := s.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM product_branch_stock
		  WHERE tenant_id = $1 AND product_id = $2 AND stock_qty <> 0`,
		tenantID, productID).Scan(&branchesWithStock); e != nil {
		return 0, 0, e
	}
	return openOrders, branchesWithStock, nil
}

// update aplica cambios parciales solo si el producto es del negocio.
// categoryID: nil = no cambia; valor = asigna (validado antes en el service).
func (s *store) update(ctx context.Context, tenantID, id string, name *string, priceCents *int, categoryID, status, imageURL, fulfillmentType *string) (Product, error) {
	var p Product
	err := s.pool.QueryRow(ctx,
		`UPDATE products
		    SET name             = COALESCE($3, name),
		        price_cents      = COALESCE($4, price_cents),
		        category_id      = COALESCE($5::uuid, category_id),
		        status           = COALESCE($6, status),
		        image_url        = COALESCE($7, image_url),
		        fulfillment_type = COALESCE($8, fulfillment_type)
		  WHERE id = $1 AND tenant_id = $2
		  RETURNING id::text, tenant_id::text, category_id::text, name, price_cents, status, image_url, fulfillment_type, created_at`,
		id, tenantID, name, priceCents, categoryID, status, imageURL, fulfillmentType).
		Scan(&p.ID, &p.TenantID, &p.CategoryID, &p.Name, &p.PriceCents, &p.Status, &p.ImageURL, &p.FulfillmentType, &p.CreatedAt)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return Product{}, ErrNotFound
	case dberr.IsInvalidText(err):
		return Product{}, ErrNotFound
	case dberr.IsUniqueViolation(err):
		return Product{}, ErrNameTaken
	case err != nil:
		return Product{}, err
	}
	return p, nil
}
