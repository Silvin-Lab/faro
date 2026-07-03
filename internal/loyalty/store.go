package loyalty

import (
	"context"
	"errors"
	"sort"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"faro/internal/dberr"
)

var ErrNotFound = errors.New("not found")

type store struct {
	pool *pgxpool.Pool
}

func newStore(pool *pgxpool.Pool) *store {
	return &store{pool: pool}
}

// list devuelve las promociones del negocio. status: "active" | "inactive" | "all".
func (s *store) list(ctx context.Context, tenantID, status string) ([]Promotion, error) {
	q := `SELECT id::text, name, discount_percent, visit_threshold, resets_counter, status, created_at, updated_at
	        FROM loyalty_promotions WHERE tenant_id = $1`
	args := []any{tenantID}
	if status == "active" || status == "inactive" {
		q += " AND status = $2"
		args = append(args, status)
	}
	q += " ORDER BY created_at DESC"

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Promotion{}
	byID := map[string]int{}
	for rows.Next() {
		var p Promotion
		if err := rows.Scan(&p.ID, &p.Name, &p.DiscountPercent, &p.VisitThreshold, &p.ResetsCounter, &p.Status, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		p.ProductIDs = []string{}
		byID[p.ID] = len(out)
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Cargar los productos de todas las promociones en una sola consulta.
	prows, err := s.pool.Query(ctx,
		`SELECT promotion_id::text, product_id::text
		   FROM loyalty_promotion_products WHERE tenant_id = $1 ORDER BY product_id`, tenantID)
	if err != nil {
		return nil, err
	}
	defer prows.Close()
	for prows.Next() {
		var promoID, prodID string
		if err := prows.Scan(&promoID, &prodID); err != nil {
			return nil, err
		}
		if i, ok := byID[promoID]; ok {
			out[i].ProductIDs = append(out[i].ProductIDs, prodID)
		}
	}
	return out, prows.Err()
}

// get devuelve una promoción del negocio con sus productos.
func (s *store) get(ctx context.Context, tenantID, id string) (Promotion, error) {
	var p Promotion
	err := s.pool.QueryRow(ctx,
		`SELECT id::text, name, discount_percent, visit_threshold, resets_counter, status, created_at, updated_at
		   FROM loyalty_promotions WHERE id = $1 AND tenant_id = $2`, id, tenantID).
		Scan(&p.ID, &p.Name, &p.DiscountPercent, &p.VisitThreshold, &p.ResetsCounter, &p.Status, &p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) || dberr.IsInvalidText(err) {
		return Promotion{}, ErrNotFound
	}
	if err != nil {
		return Promotion{}, err
	}
	p.ProductIDs, err = s.productIDs(ctx, tenantID, id)
	return p, err
}

func (s *store) productIDs(ctx context.Context, tenantID, promotionID string) ([]string, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT product_id::text FROM loyalty_promotion_products
		  WHERE tenant_id = $1 AND promotion_id = $2 ORDER BY product_id`, tenantID, promotionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// create inserta una promoción con sus productos. Rechaza si algún producto no es
// del negocio (ErrValidation).
func (s *store) create(ctx context.Context, tenantID string, in PromotionInput) (Promotion, error) {
	ids := dedupe(in.ProductIDs)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Promotion{}, err
	}
	defer tx.Rollback(ctx)

	if err := verifyOwnedProducts(ctx, tx, tenantID, ids); err != nil {
		return Promotion{}, err
	}

	var p Promotion
	if err := tx.QueryRow(ctx,
		`INSERT INTO loyalty_promotions (tenant_id, name, discount_percent, visit_threshold, resets_counter)
		 VALUES ($1, $2, $3, $4, $5)
		 RETURNING id::text, name, discount_percent, visit_threshold, resets_counter, status, created_at, updated_at`,
		tenantID, in.Name, in.DiscountPercent, in.VisitThreshold, in.ResetsCounter).
		Scan(&p.ID, &p.Name, &p.DiscountPercent, &p.VisitThreshold, &p.ResetsCounter, &p.Status, &p.CreatedAt, &p.UpdatedAt); err != nil {
		return Promotion{}, err
	}
	if err := insertProducts(ctx, tx, tenantID, p.ID, ids); err != nil {
		return Promotion{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Promotion{}, err
	}
	p.ProductIDs = ids
	return p, nil
}

// update reemplaza datos y productos de una promoción existente del negocio.
func (s *store) update(ctx context.Context, tenantID, id string, in PromotionInput) (Promotion, error) {
	ids := dedupe(in.ProductIDs)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Promotion{}, err
	}
	defer tx.Rollback(ctx)

	if err := verifyOwnedProducts(ctx, tx, tenantID, ids); err != nil {
		return Promotion{}, err
	}

	var p Promotion
	err = tx.QueryRow(ctx,
		`UPDATE loyalty_promotions
		    SET name = $3, discount_percent = $4, visit_threshold = $5, resets_counter = $6, updated_at = now()
		  WHERE id = $1 AND tenant_id = $2
		 RETURNING id::text, name, discount_percent, visit_threshold, resets_counter, status, created_at, updated_at`,
		id, tenantID, in.Name, in.DiscountPercent, in.VisitThreshold, in.ResetsCounter).
		Scan(&p.ID, &p.Name, &p.DiscountPercent, &p.VisitThreshold, &p.ResetsCounter, &p.Status, &p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) || dberr.IsInvalidText(err) {
		return Promotion{}, ErrNotFound
	}
	if err != nil {
		return Promotion{}, err
	}

	if _, err := tx.Exec(ctx,
		`DELETE FROM loyalty_promotion_products WHERE tenant_id = $1 AND promotion_id = $2`, tenantID, id); err != nil {
		return Promotion{}, err
	}
	if err := insertProducts(ctx, tx, tenantID, id, ids); err != nil {
		return Promotion{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Promotion{}, err
	}
	p.ProductIDs = ids
	return p, nil
}

// archive baja una promoción (soft delete): status = 'inactive'.
func (s *store) archive(ctx context.Context, tenantID, id string) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE loyalty_promotions SET status = 'inactive', updated_at = now()
		  WHERE id = $1 AND tenant_id = $2`, id, tenantID)
	if dberr.IsInvalidText(err) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// customerStatus devuelve las visitas del cliente y la elegibilidad por cada
// promoción activa, ordenadas ascendente por visitsRemaining (aplicables primero).
func (s *store) customerStatus(ctx context.Context, tenantID, customerID string) (CustomerStatus, error) {
	st := CustomerStatus{CustomerID: customerID, Promotions: []PromotionStatus{}}
	err := s.pool.QueryRow(ctx,
		`SELECT visits, visits_lifetime FROM customers WHERE id = $1 AND tenant_id = $2`,
		customerID, tenantID).Scan(&st.Visits, &st.VisitsLifetime)
	if errors.Is(err, pgx.ErrNoRows) || dberr.IsInvalidText(err) {
		return CustomerStatus{}, ErrNotFound
	}
	if err != nil {
		return CustomerStatus{}, err
	}

	rows, err := s.pool.Query(ctx,
		`SELECT id::text, name, discount_percent, visit_threshold, resets_counter
		   FROM loyalty_promotions WHERE tenant_id = $1 AND status = 'active'`, tenantID)
	if err != nil {
		return CustomerStatus{}, err
	}
	defer rows.Close()

	byID := map[string]int{}
	for rows.Next() {
		var ps PromotionStatus
		if err := rows.Scan(&ps.PromotionID, &ps.Name, &ps.DiscountPercent, &ps.VisitThreshold, &ps.ResetsCounter); err != nil {
			return CustomerStatus{}, err
		}
		ps.VisitsRemaining = ps.VisitThreshold - st.Visits
		if ps.VisitsRemaining < 0 {
			ps.VisitsRemaining = 0
		}
		ps.ApplicableNow = st.Visits+1 >= ps.VisitThreshold
		ps.Products = []PromoProduct{}
		byID[ps.PromotionID] = len(st.Promotions)
		st.Promotions = append(st.Promotions, ps)
	}
	if err := rows.Err(); err != nil {
		return CustomerStatus{}, err
	}

	// Productos de todas las promociones activas en una sola consulta.
	prows, err := s.pool.Query(ctx,
		`SELECT lpp.promotion_id::text, p.id::text, p.name, p.price_cents
		   FROM loyalty_promotion_products lpp
		   JOIN products p ON p.id = lpp.product_id
		   JOIN loyalty_promotions lp ON lp.id = lpp.promotion_id AND lp.status = 'active'
		  WHERE lpp.tenant_id = $1
		  ORDER BY p.name`, tenantID)
	if err != nil {
		return CustomerStatus{}, err
	}
	defer prows.Close()
	for prows.Next() {
		var promoID string
		var pp PromoProduct
		if err := prows.Scan(&promoID, &pp.ID, &pp.Name, &pp.PriceCents); err != nil {
			return CustomerStatus{}, err
		}
		if i, ok := byID[promoID]; ok {
			st.Promotions[i].Products = append(st.Promotions[i].Products, pp)
		}
	}
	if err := prows.Err(); err != nil {
		return CustomerStatus{}, err
	}

	// Orden ascendente por visitsRemaining (aplicables primero); desempate por nombre.
	sort.SliceStable(st.Promotions, func(i, j int) bool {
		a, b := st.Promotions[i], st.Promotions[j]
		if a.VisitsRemaining != b.VisitsRemaining {
			return a.VisitsRemaining < b.VisitsRemaining
		}
		return a.Name < b.Name
	})
	return st, nil
}

// verifyOwnedProducts falla con ErrValidation si algún id no es un producto del
// negocio (o si la lista está vacía).
func verifyOwnedProducts(ctx context.Context, tx pgx.Tx, tenantID string, ids []string) error {
	if len(ids) == 0 {
		return ErrValidation
	}
	var owned int
	if err := tx.QueryRow(ctx,
		`SELECT count(DISTINCT id) FROM products WHERE tenant_id = $1 AND id = ANY($2::uuid[])`,
		tenantID, ids).Scan(&owned); err != nil {
		return err
	}
	if owned != len(ids) {
		return ErrValidation
	}
	return nil
}

func insertProducts(ctx context.Context, tx pgx.Tx, tenantID, promotionID string, ids []string) error {
	for _, pid := range ids {
		if _, err := tx.Exec(ctx,
			`INSERT INTO loyalty_promotion_products (promotion_id, product_id, tenant_id)
			 VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`, promotionID, pid, tenantID); err != nil {
			return err
		}
	}
	return nil
}

func dedupe(ids []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}
