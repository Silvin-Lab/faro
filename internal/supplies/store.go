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
	ErrNotFound        = errors.New("not found")
	ErrNameTaken       = errors.New("name taken")
	ErrInvalidBranch   = errors.New("invalid branch")
	ErrInvalidSupply   = errors.New("invalid supply")
	ErrInvalidCategory = errors.New("invalid category")
	ErrInvalidMeasure  = errors.New("invalid measure")
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

// ---- Categorías de insumo (espejo de expense_categories) -------------------

func (s *store) createCategory(ctx context.Context, tenantID, name string, sortOrder int) (SupplyCategory, error) {
	var c SupplyCategory
	err := s.pool.QueryRow(ctx,
		`INSERT INTO supply_categories (tenant_id, name, sort_order)
		 VALUES ($1, $2, $3)
		 RETURNING id::text, tenant_id::text, name, status, sort_order, created_at`,
		tenantID, name, sortOrder).
		Scan(&c.ID, &c.TenantID, &c.Name, &c.Status, &c.SortOrder, &c.CreatedAt)
	if pgCode(err, "23505") {
		return SupplyCategory{}, ErrNameTaken
	}
	return c, err
}

func (s *store) listCategories(ctx context.Context, tenantID string) ([]SupplyCategory, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id::text, tenant_id::text, name, status, sort_order, created_at
		   FROM supply_categories WHERE tenant_id = $1 ORDER BY sort_order, name`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []SupplyCategory
	for rows.Next() {
		var c SupplyCategory
		if err := rows.Scan(&c.ID, &c.TenantID, &c.Name, &c.Status, &c.SortOrder, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *store) updateCategory(ctx context.Context, tenantID, id string, name, status *string, sortOrder *int) (SupplyCategory, error) {
	var c SupplyCategory
	err := s.pool.QueryRow(ctx,
		`UPDATE supply_categories
		    SET name       = COALESCE($3, name),
		        status     = COALESCE($4, status),
		        sort_order = COALESCE($5, sort_order)
		  WHERE id = $1 AND tenant_id = $2
		  RETURNING id::text, tenant_id::text, name, status, sort_order, created_at`,
		id, tenantID, name, status, sortOrder).
		Scan(&c.ID, &c.TenantID, &c.Name, &c.Status, &c.SortOrder, &c.CreatedAt)
	switch {
	case errors.Is(err, pgx.ErrNoRows), pgCode(err, "22P02"):
		return SupplyCategory{}, ErrNotFound
	case pgCode(err, "23505"):
		return SupplyCategory{}, ErrNameTaken
	case err != nil:
		return SupplyCategory{}, err
	}
	return c, nil
}

// categoryExists indica si la categoría pertenece al tenant. Un uuid mal formado
// (22P02) se trata como inexistente.
func (s *store) categoryExists(ctx context.Context, tenantID, id string) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM supply_categories WHERE id = $1 AND tenant_id = $2)`,
		id, tenantID).Scan(&ok)
	if pgCode(err, "22P02") {
		return false, nil
	}
	return ok, err
}

// ---- Insumos (catálogo) ----------------------------------------------------

func (s *store) create(ctx context.Context, tenantID, name, baseUnit, packageName string, packageContent int, packageCostCents *int, categoryID *string) (Supply, error) {
	var sp Supply
	err := s.pool.QueryRow(ctx,
		`WITH ins AS (
		     INSERT INTO supplies (tenant_id, name, base_unit, package_name, package_content, package_cost_cents, category_id)
		     VALUES ($1, $2, $3, $4, $5, $6, $7)
		     RETURNING id, tenant_id, name, base_unit, package_name, package_content, package_cost_cents, category_id, status, created_at
		 )
		 SELECT i.id::text, i.tenant_id::text, i.name, i.base_unit, i.package_name, i.package_content,
		        i.package_cost_cents, i.category_id::text, cat.name, i.status, i.created_at
		   FROM ins i
		   LEFT JOIN supply_categories cat ON cat.id = i.category_id`,
		tenantID, name, baseUnit, packageName, packageContent, packageCostCents, categoryID).
		Scan(&sp.ID, &sp.TenantID, &sp.Name, &sp.BaseUnit, &sp.PackageName, &sp.PackageContent,
			&sp.PackageCostCents, &sp.CategoryID, &sp.CategoryName, &sp.Status, &sp.CreatedAt)
	if pgCode(err, "23505") {
		return Supply{}, ErrNameTaken
	}
	sp.Stock = []BranchStock{}
	sp.Measures = []SupplyMeasure{}
	return sp, err
}

// get devuelve un insumo del tenant (sin existencias) con sus medidas de uso. uuid
// mal formado => not found.
func (s *store) get(ctx context.Context, tenantID, id string) (Supply, error) {
	var sp Supply
	err := s.pool.QueryRow(ctx,
		`SELECT sp.id::text, sp.tenant_id::text, sp.name, sp.base_unit, sp.package_name, sp.package_content,
		        sp.package_cost_cents, sp.category_id::text, cat.name, sp.status, sp.created_at
		   FROM supplies sp
		   LEFT JOIN supply_categories cat ON cat.id = sp.category_id
		  WHERE sp.id = $1 AND sp.tenant_id = $2`, id, tenantID).
		Scan(&sp.ID, &sp.TenantID, &sp.Name, &sp.BaseUnit, &sp.PackageName, &sp.PackageContent,
			&sp.PackageCostCents, &sp.CategoryID, &sp.CategoryName, &sp.Status, &sp.CreatedAt)
	switch {
	case errors.Is(err, pgx.ErrNoRows), pgCode(err, "22P02"):
		return Supply{}, ErrNotFound
	case err != nil:
		return Supply{}, err
	}
	sp.Stock = []BranchStock{}
	measures, err := s.listMeasures(ctx, tenantID, sp.ID)
	if err != nil {
		return Supply{}, err
	}
	sp.Measures = measures
	return sp, nil
}

// update aplica cambios parciales SOLO si el insumo es del tenant. base_unit NO se
// toca aquí (inmutable; la capa service rechaza el intento). setCategory=true
// reemplaza category_id por categoryID (ya validado como del tenant en service);
// false lo deja intacto (el puntero nil = "no cambia" no se puede distinguir de
// "poner a null" con un solo campo, así que PATCH solo asigna, no desasigna).
func (s *store) update(ctx context.Context, tenantID, id string, name, status, packageName *string, packageContent, packageCostCents *int, categoryID *string, setCategory bool) (Supply, error) {
	var sp Supply
	err := s.pool.QueryRow(ctx,
		`WITH upd AS (
		     UPDATE supplies
		        SET name               = COALESCE($3, name),
		            status             = COALESCE($4, status),
		            package_name       = COALESCE($5, package_name),
		            package_content    = COALESCE($6, package_content),
		            package_cost_cents = COALESCE($7, package_cost_cents),
		            category_id        = CASE WHEN $9 THEN $8 ELSE category_id END
		      WHERE id = $1 AND tenant_id = $2
		      RETURNING id, tenant_id, name, base_unit, package_name, package_content, package_cost_cents, category_id, status, created_at
		 )
		 SELECT u.id::text, u.tenant_id::text, u.name, u.base_unit, u.package_name, u.package_content,
		        u.package_cost_cents, u.category_id::text, cat.name, u.status, u.created_at
		   FROM upd u
		   LEFT JOIN supply_categories cat ON cat.id = u.category_id`,
		id, tenantID, name, status, packageName, packageContent, packageCostCents, categoryID, setCategory).
		Scan(&sp.ID, &sp.TenantID, &sp.Name, &sp.BaseUnit, &sp.PackageName, &sp.PackageContent,
			&sp.PackageCostCents, &sp.CategoryID, &sp.CategoryName, &sp.Status, &sp.CreatedAt)
	switch {
	case errors.Is(err, pgx.ErrNoRows), pgCode(err, "22P02"):
		return Supply{}, ErrNotFound
	case pgCode(err, "23505"):
		return Supply{}, ErrNameTaken
	case err != nil:
		return Supply{}, err
	}
	sp.Stock = []BranchStock{}
	measures, err := s.listMeasures(ctx, tenantID, sp.ID)
	if err != nil {
		return Supply{}, err
	}
	sp.Measures = measures
	return sp, nil
}

// list devuelve los insumos del tenant con sus existencias por sucursal. Se hacen
// dos consultas (evita N+1): insumos + todas las filas de cache del tenant, y se
// agrupan en memoria. Solo aparecen las sucursales con fila en supply_branch_stock;
// una sucursal ausente = 0 (lo interpreta la UI). Ver decisión en model.go.
func (s *store) list(ctx context.Context, tenantID string) ([]Supply, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT sp.id::text, sp.tenant_id::text, sp.name, sp.base_unit, sp.package_name, sp.package_content,
		        sp.package_cost_cents, sp.category_id::text, cat.name, sp.status, sp.created_at
		   FROM supplies sp
		   LEFT JOIN supply_categories cat ON cat.id = sp.category_id
		  WHERE sp.tenant_id = $1 ORDER BY sp.name`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Supply
	idx := map[string]int{}
	for rows.Next() {
		var sp Supply
		if err := rows.Scan(&sp.ID, &sp.TenantID, &sp.Name, &sp.BaseUnit, &sp.PackageName, &sp.PackageContent,
			&sp.PackageCostCents, &sp.CategoryID, &sp.CategoryName, &sp.Status, &sp.CreatedAt); err != nil {
			return nil, err
		}
		sp.Stock = []BranchStock{}
		sp.Measures = []SupplyMeasure{}
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
	if err := stockRows.Err(); err != nil {
		return nil, err
	}

	// Medidas de uso: 2ª query agrupada (evita N+1, como el stock). Solo aparecen
	// las medidas de insumos del tenant; se agrupan en memoria por supply_id.
	measureRows, err := s.pool.Query(ctx,
		`SELECT id::text, supply_id::text, name, base_quantity
		   FROM supply_measures
		  WHERE tenant_id = $1
		  ORDER BY name`, tenantID)
	if err != nil {
		return nil, err
	}
	defer measureRows.Close()
	for measureRows.Next() {
		var m SupplyMeasure
		if err := measureRows.Scan(&m.ID, &m.SupplyID, &m.Name, &m.BaseQuantity); err != nil {
			return nil, err
		}
		if i, ok := idx[m.SupplyID]; ok {
			out[i].Measures = append(out[i].Measures, m)
		}
	}
	return out, measureRows.Err()
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

	stockBase, err := UpsertBranchStock(ctx, tx, tenantID, supplyID, branchID, delta)
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

// ---- Medidas de uso --------------------------------------------------------

// listMeasures devuelve las medidas de un insumo del tenant, ordenadas por nombre.
// uuid mal formado => lista vacía (sin error).
func (s *store) listMeasures(ctx context.Context, tenantID, supplyID string) ([]SupplyMeasure, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id::text, supply_id::text, name, base_quantity
		   FROM supply_measures
		  WHERE tenant_id = $1 AND supply_id = $2
		  ORDER BY name`, tenantID, supplyID)
	if pgCode(err, "22P02") {
		return []SupplyMeasure{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []SupplyMeasure{}
	for rows.Next() {
		var m SupplyMeasure
		if err := rows.Scan(&m.ID, &m.SupplyID, &m.Name, &m.BaseQuantity); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// createMeasure inserta una medida para un insumo del tenant. Nombre duplicado por
// insumo => ErrNameTaken (UNIQUE supply_id,name). El insumo ya fue validado del
// tenant en la capa service.
func (s *store) createMeasure(ctx context.Context, tenantID, supplyID, name string, baseQuantity int) (SupplyMeasure, error) {
	var m SupplyMeasure
	err := s.pool.QueryRow(ctx,
		`INSERT INTO supply_measures (tenant_id, supply_id, name, base_quantity)
		 VALUES ($1, $2, $3, $4)
		 RETURNING id::text, supply_id::text, name, base_quantity`,
		tenantID, supplyID, name, baseQuantity).
		Scan(&m.ID, &m.SupplyID, &m.Name, &m.BaseQuantity)
	if pgCode(err, "23505") {
		return SupplyMeasure{}, ErrNameTaken
	}
	return m, err
}

// updateMeasure aplica cambios parciales a una medida del tenant. Si baseQuantity
// cambia, en la MISMA transacción recomputa el quantity_base de las recetas que usan
// esta medida: quantity_base = GREATEST(1, ROUND(measure_count * nuevo)). Esto
// propaga el "en vivo" a consumo y costo SIN tocar el descuento en venta (que sigue
// leyendo quantity_base). uuid mal formado / ajeno => ErrNotFound.
func (s *store) updateMeasure(ctx context.Context, tenantID, measureID string, name *string, baseQuantity *int) (SupplyMeasure, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return SupplyMeasure{}, err
	}
	defer tx.Rollback(ctx)

	var m SupplyMeasure
	err = tx.QueryRow(ctx,
		`UPDATE supply_measures
		    SET name          = COALESCE($3, name),
		        base_quantity = COALESCE($4, base_quantity)
		  WHERE id = $1 AND tenant_id = $2
		  RETURNING id::text, supply_id::text, name, base_quantity`,
		measureID, tenantID, name, baseQuantity).
		Scan(&m.ID, &m.SupplyID, &m.Name, &m.BaseQuantity)
	switch {
	case errors.Is(err, pgx.ErrNoRows), pgCode(err, "22P02"):
		return SupplyMeasure{}, ErrNotFound
	case pgCode(err, "23505"):
		return SupplyMeasure{}, ErrNameTaken
	case err != nil:
		return SupplyMeasure{}, err
	}

	// Recompute-on-edit: solo cuando cambia baseQuantity. GREATEST(1, ...) respeta el
	// CHECK(quantity_base > 0) aun con medidas/counts diminutos.
	if baseQuantity != nil {
		if _, err := tx.Exec(ctx,
			`UPDATE product_supplies
			    SET quantity_base = GREATEST(1, ROUND(measure_count * $2))
			  WHERE measure_id = $1 AND tenant_id = $3`,
			measureID, *baseQuantity, tenantID); err != nil {
			return SupplyMeasure{}, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return SupplyMeasure{}, err
	}
	return m, nil
}

// deleteMeasure borra una medida del tenant. ON DELETE SET NULL en product_supplies:
// las recetas que la usaban conservan su quantity_base "congelado" (measure_id/
// measure_count quedan NULL). uuid mal formado / ajeno => ErrNotFound.
func (s *store) deleteMeasure(ctx context.Context, tenantID, measureID string) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM supply_measures WHERE id = $1 AND tenant_id = $2`,
		measureID, tenantID)
	if pgCode(err, "22P02") {
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

// measureForSupply devuelve base_quantity de una medida SI pertenece al insumo y al
// tenant indicados. ok=false (sin error) si no existe / uuid mal formado / ajena.
func (s *store) measureForSupply(ctx context.Context, tenantID, supplyID, measureID string) (int, bool, error) {
	var base int
	err := s.pool.QueryRow(ctx,
		`SELECT base_quantity FROM supply_measures
		  WHERE id = $1 AND supply_id = $2 AND tenant_id = $3`,
		measureID, supplyID, tenantID).Scan(&base)
	switch {
	case errors.Is(err, pgx.ErrNoRows), pgCode(err, "22P02"):
		return 0, false, nil
	case err != nil:
		return 0, false, err
	}
	return base, true, nil
}

// supplyInTenant indica si el insumo pertenece al tenant. uuid mal formado => false.
func (s *store) supplyInTenant(ctx context.Context, tenantID, supplyID string) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM supplies WHERE id = $1 AND tenant_id = $2)`,
		supplyID, tenantID).Scan(&ok)
	if pgCode(err, "22P02") {
		return false, nil
	}
	return ok, err
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
		`SELECT ps.supply_id::text, sp.name, sp.base_unit, ps.quantity_base,
		        ps.measure_id::text, m.name,
		        CASE WHEN ps.measure_id IS NOT NULL THEN ps.measure_count END
		   FROM product_supplies ps
		   JOIN supplies sp ON sp.id = ps.supply_id
		   LEFT JOIN supply_measures m ON m.id = ps.measure_id
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
		if err := rows.Scan(&it.SupplyID, &it.SupplyName, &it.BaseUnit, &it.QuantityBase,
			&it.MeasureID, &it.MeasureName, &it.MeasureCount); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// recipeItemIn es la entrada de una línea de receta a persistir. Si MeasureID viene,
// la línea se capturó por medida y QuantityBase ya fue computado (ROUND(count*base))
// y validado (>= 1) en la capa service; MeasureCount se persiste como registro del
// "cómo se capturó". Si MeasureID es nil, es una línea en unidad base (como hoy).
type recipeItemIn struct {
	SupplyID     string
	QuantityBase int
	MeasureID    *string
	MeasureCount *float64
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
			`INSERT INTO product_supplies (tenant_id, product_id, supply_id, quantity_base, measure_id, measure_count)
			 SELECT $1, $2, sp.id, $4, $5, $6
			   FROM supplies sp
			  WHERE sp.id = $3 AND sp.tenant_id = $1`,
			tenantID, productID, it.SupplyID, it.QuantityBase, it.MeasureID, it.MeasureCount)
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
		`SELECT ps.supply_id::text, sp.name, sp.base_unit, ps.quantity_base,
		        ps.measure_id::text, m.name,
		        CASE WHEN ps.measure_id IS NOT NULL THEN ps.measure_count END
		   FROM product_supplies ps
		   JOIN supplies sp ON sp.id = ps.supply_id
		   LEFT JOIN supply_measures m ON m.id = ps.measure_id
		  WHERE ps.tenant_id = $1 AND ps.product_id = $2
		  ORDER BY sp.name`, tenantID, productID)
	if err != nil {
		return nil, err
	}
	var out []RecipeItem
	for rows.Next() {
		var it RecipeItem
		if err := rows.Scan(&it.SupplyID, &it.SupplyName, &it.BaseUnit, &it.QuantityBase,
			&it.MeasureID, &it.MeasureName, &it.MeasureCount); err != nil {
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
