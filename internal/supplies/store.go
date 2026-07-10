package supplies

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound      = errors.New("not found")
	ErrNameTaken     = errors.New("name taken")
	ErrInvalidBranch = errors.New("invalid branch")
	ErrInvalidSupply = errors.New("invalid supply")
)

type store struct {
	pool *pgxpool.Pool
}

func newStore(pool *pgxpool.Pool) *store {
	return &store{pool: pool}
}

func pgCode(err error, code string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == code
}

// ---- Insumos (catálogo) ----------------------------------------------------

func (s *store) create(ctx context.Context, tenantID, name, baseUnit, packageName string, packageContent int, packageCostCents *int) (Supply, error) {
	var sp Supply
	err := s.pool.QueryRow(ctx,
		`INSERT INTO supplies (tenant_id, name, base_unit, package_name, package_content, package_cost_cents)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 RETURNING id::text, tenant_id::text, name, base_unit, package_name, package_content, package_cost_cents, status, created_at`,
		tenantID, name, baseUnit, packageName, packageContent, packageCostCents).
		Scan(&sp.ID, &sp.TenantID, &sp.Name, &sp.BaseUnit, &sp.PackageName, &sp.PackageContent, &sp.PackageCostCents, &sp.Status, &sp.CreatedAt)
	if pgCode(err, "23505") {
		return Supply{}, ErrNameTaken
	}
	sp.Stock = []BranchStock{}
	return sp, err
}

// get devuelve un insumo del tenant (sin existencias). uuid mal formado => not found.
func (s *store) get(ctx context.Context, tenantID, id string) (Supply, error) {
	var sp Supply
	err := s.pool.QueryRow(ctx,
		`SELECT id::text, tenant_id::text, name, base_unit, package_name, package_content, package_cost_cents, status, created_at
		   FROM supplies WHERE id = $1 AND tenant_id = $2`, id, tenantID).
		Scan(&sp.ID, &sp.TenantID, &sp.Name, &sp.BaseUnit, &sp.PackageName, &sp.PackageContent, &sp.PackageCostCents, &sp.Status, &sp.CreatedAt)
	switch {
	case errors.Is(err, pgx.ErrNoRows), pgCode(err, "22P02"):
		return Supply{}, ErrNotFound
	case err != nil:
		return Supply{}, err
	}
	sp.Stock = []BranchStock{}
	return sp, nil
}

// update aplica cambios parciales SOLO si el insumo es del tenant. base_unit NO se
// toca aquí (inmutable; la capa service rechaza el intento).
func (s *store) update(ctx context.Context, tenantID, id string, name, status, packageName *string, packageContent, packageCostCents *int) (Supply, error) {
	var sp Supply
	err := s.pool.QueryRow(ctx,
		`UPDATE supplies
		    SET name               = COALESCE($3, name),
		        status             = COALESCE($4, status),
		        package_name       = COALESCE($5, package_name),
		        package_content    = COALESCE($6, package_content),
		        package_cost_cents = COALESCE($7, package_cost_cents)
		  WHERE id = $1 AND tenant_id = $2
		  RETURNING id::text, tenant_id::text, name, base_unit, package_name, package_content, package_cost_cents, status, created_at`,
		id, tenantID, name, status, packageName, packageContent, packageCostCents).
		Scan(&sp.ID, &sp.TenantID, &sp.Name, &sp.BaseUnit, &sp.PackageName, &sp.PackageContent, &sp.PackageCostCents, &sp.Status, &sp.CreatedAt)
	switch {
	case errors.Is(err, pgx.ErrNoRows), pgCode(err, "22P02"):
		return Supply{}, ErrNotFound
	case pgCode(err, "23505"):
		return Supply{}, ErrNameTaken
	case err != nil:
		return Supply{}, err
	}
	sp.Stock = []BranchStock{}
	return sp, nil
}

// list devuelve los insumos del tenant con sus existencias por sucursal. Se hacen
// dos consultas (evita N+1): insumos + todas las filas de cache del tenant, y se
// agrupan en memoria. Solo aparecen las sucursales con fila en supply_branch_stock;
// una sucursal ausente = 0 (lo interpreta la UI). Ver decisión en model.go.
func (s *store) list(ctx context.Context, tenantID string) ([]Supply, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id::text, tenant_id::text, name, base_unit, package_name, package_content, package_cost_cents, status, created_at
		   FROM supplies WHERE tenant_id = $1 ORDER BY name`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Supply
	idx := map[string]int{}
	for rows.Next() {
		var sp Supply
		if err := rows.Scan(&sp.ID, &sp.TenantID, &sp.Name, &sp.BaseUnit, &sp.PackageName, &sp.PackageContent, &sp.PackageCostCents, &sp.Status, &sp.CreatedAt); err != nil {
			return nil, err
		}
		sp.Stock = []BranchStock{}
		idx[sp.ID] = len(out)
		out = append(out, sp)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	stockRows, err := s.pool.Query(ctx,
		`SELECT sbs.supply_id::text, sbs.branch_id::text, b.name, sbs.stock_base
		   FROM supply_branch_stock sbs
		   JOIN branches b ON b.id = sbs.branch_id
		  WHERE sbs.tenant_id = $1
		  ORDER BY b.name`, tenantID)
	if err != nil {
		return nil, err
	}
	defer stockRows.Close()
	for stockRows.Next() {
		var supplyID string
		var bs BranchStock
		if err := stockRows.Scan(&supplyID, &bs.BranchID, &bs.BranchName, &bs.StockBase); err != nil {
			return nil, err
		}
		if i, ok := idx[supplyID]; ok {
			out[i].Stock = append(out[i].Stock, bs)
		}
	}
	return out, stockRows.Err()
}

// branchInTenant indica si la sucursal pertenece al tenant. uuid mal formado => false.
func (s *store) branchInTenant(ctx context.Context, tenantID, branchID string) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM branches WHERE id = $1 AND tenant_id = $2)`,
		branchID, tenantID).Scan(&ok)
	if pgCode(err, "22P02") {
		return false, nil
	}
	return ok, err
}

// ---- Movimientos (ledger + cache) ------------------------------------------

// insertMovement escribe el movimiento y actualiza el cache de existencias de la
// sucursal en UNA transacción (sin ventana de inconsistencia). delta va firmado.
// Devuelve el movimiento con nombres derivados y el nuevo stock de la sucursal. No
// valida existencias: el stock puede quedar negativo.
func (s *store) insertMovement(ctx context.Context, tenantID, supplyID, branchID, mvType string, delta int, reason *string, createdBy string) (Movement, int, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Movement{}, 0, err
	}
	defer tx.Rollback(ctx)

	var m Movement
	err = tx.QueryRow(ctx,
		`WITH ins AS (
		     INSERT INTO supply_movements (tenant_id, supply_id, branch_id, type, quantity_base, reason, created_by)
		     VALUES ($1, $2, $3, $4, $5, $6, $7)
		     RETURNING id, tenant_id, supply_id, branch_id, type, quantity_base, reason, sale_id, created_by, created_at
		 )
		 SELECT i.id::text, i.tenant_id::text, i.supply_id::text, i.branch_id::text, b.name,
		        i.type, i.quantity_base, i.reason, i.sale_id::text, i.created_by::text, u.name, i.created_at
		   FROM ins i
		   LEFT JOIN branches b ON b.id = i.branch_id
		   LEFT JOIN users u ON u.id = i.created_by`,
		tenantID, supplyID, branchID, mvType, delta, reason, createdBy).
		Scan(&m.ID, &m.TenantID, &m.SupplyID, &m.BranchID, &m.BranchName, &m.Type,
			&m.QuantityBase, &m.Reason, &m.SaleID, &m.CreatedBy, &m.CreatedByName, &m.CreatedAt)
	if err != nil {
		return Movement{}, 0, err
	}

	var stockBase int
	err = tx.QueryRow(ctx,
		`INSERT INTO supply_branch_stock (tenant_id, supply_id, branch_id, stock_base)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (supply_id, branch_id)
		 DO UPDATE SET stock_base = supply_branch_stock.stock_base + EXCLUDED.stock_base,
		               updated_at = now()
		 RETURNING stock_base`,
		tenantID, supplyID, branchID, delta).Scan(&stockBase)
	if err != nil {
		return Movement{}, 0, err
	}

	if err := tx.Commit(ctx); err != nil {
		return Movement{}, 0, err
	}
	return m, stockBase, nil
}

// listMovements lista el ledger de un insumo del tenant (rango [from,to) opcional).
// LIMIT 100 DESC. created_by NULL (venta automática) => createdByName NULL.
func (s *store) listMovements(ctx context.Context, tenantID, supplyID string, from, to *time.Time) ([]Movement, error) {
	args := []any{tenantID, supplyID}
	q := `SELECT m.id::text, m.tenant_id::text, m.supply_id::text, m.branch_id::text, b.name,
	             m.type, m.quantity_base, m.reason, m.sale_id::text, m.created_by::text, u.name, m.created_at
	        FROM supply_movements m
	        LEFT JOIN branches b ON b.id = m.branch_id
	        LEFT JOIN users u ON u.id = m.created_by
	       WHERE m.tenant_id = $1 AND m.supply_id = $2`
	if from != nil {
		args = append(args, *from)
		q += ` AND m.created_at >= $` + strconv.Itoa(len(args))
	}
	if to != nil {
		args = append(args, *to)
		q += ` AND m.created_at < $` + strconv.Itoa(len(args))
	}
	q += ` ORDER BY m.created_at DESC LIMIT 100`

	rows, err := s.pool.Query(ctx, q, args...)
	if pgCode(err, "22P02") {
		return []Movement{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Movement
	for rows.Next() {
		var m Movement
		if err := rows.Scan(&m.ID, &m.TenantID, &m.SupplyID, &m.BranchID, &m.BranchName, &m.Type,
			&m.QuantityBase, &m.Reason, &m.SaleID, &m.CreatedBy, &m.CreatedByName, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ---- Recetas (globales) ----------------------------------------------------

// productInTenant indica si el producto es del tenant. uuid mal formado => false.
func (s *store) productInTenant(ctx context.Context, tenantID, productID string) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM products WHERE id = $1 AND tenant_id = $2)`,
		productID, tenantID).Scan(&ok)
	if pgCode(err, "22P02") {
		return false, nil
	}
	return ok, err
}

func (s *store) getRecipe(ctx context.Context, tenantID, productID string) ([]RecipeItem, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT ps.supply_id::text, sp.name, sp.base_unit, ps.quantity_base
		   FROM product_supplies ps
		   JOIN supplies sp ON sp.id = ps.supply_id
		  WHERE ps.tenant_id = $1 AND ps.product_id = $2
		  ORDER BY sp.name`, tenantID, productID)
	if pgCode(err, "22P02") {
		return []RecipeItem{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []RecipeItem
	for rows.Next() {
		var it RecipeItem
		if err := rows.Scan(&it.SupplyID, &it.SupplyName, &it.BaseUnit, &it.QuantityBase); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// recipeItemIn es la entrada de una línea de receta a persistir.
type recipeItemIn struct {
	SupplyID     string
	QuantityBase int
}

// replaceRecipe reemplaza la receta completa del producto en una transacción
// (DELETE + INSERT). Valida que cada insumo sea del tenant (ErrInvalidSupply). El
// producto ya fue validado como del tenant en la capa service. Devuelve la receta
// resultante (con nombres) para la respuesta.
func (s *store) replaceRecipe(ctx context.Context, tenantID, productID string, items []recipeItemIn) ([]RecipeItem, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx,
		`DELETE FROM product_supplies WHERE tenant_id = $1 AND product_id = $2`,
		tenantID, productID); err != nil {
		return nil, err
	}

	for _, it := range items {
		// Cada insumo debe ser del tenant: el INSERT ... SELECT solo produce fila si
		// el insumo existe en el tenant; 0 filas => insumo ajeno/inexistente.
		tag, err := tx.Exec(ctx,
			`INSERT INTO product_supplies (tenant_id, product_id, supply_id, quantity_base)
			 SELECT $1, $2, sp.id, $4
			   FROM supplies sp
			  WHERE sp.id = $3 AND sp.tenant_id = $1`,
			tenantID, productID, it.SupplyID, it.QuantityBase)
		if pgCode(err, "22P02") { // uuid mal formado
			return nil, ErrInvalidSupply
		}
		if err != nil {
			return nil, err
		}
		if tag.RowsAffected() == 0 {
			return nil, ErrInvalidSupply
		}
	}

	rows, err := tx.Query(ctx,
		`SELECT ps.supply_id::text, sp.name, sp.base_unit, ps.quantity_base
		   FROM product_supplies ps
		   JOIN supplies sp ON sp.id = ps.supply_id
		  WHERE ps.tenant_id = $1 AND ps.product_id = $2
		  ORDER BY sp.name`, tenantID, productID)
	if err != nil {
		return nil, err
	}
	var out []RecipeItem
	for rows.Next() {
		var it RecipeItem
		if err := rows.Scan(&it.SupplyID, &it.SupplyName, &it.BaseUnit, &it.QuantityBase); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, it)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return out, nil
}
