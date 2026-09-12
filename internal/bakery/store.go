package bakery

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound       = errors.New("not found")
	ErrValidation     = errors.New("validation")
	ErrInvalidBranch  = errors.New("invalid branch")
	ErrInvalidProduct = errors.New("invalid product")
	ErrInvalidState   = errors.New("invalid state")
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

// isInvalidUUID detecta el error de texto uuid mal formado (22P02): se trata como
// inexistente (mismo criterio que el resto de módulos).
func isInvalidUUID(err error) bool { return pgCode(err, "22P02") }

const orderCols = `o.id::text, o.branch_id::text, b.name, o.product_id::text, p.name,
	o.quantity_ordered, o.quantity_shipped, o.status, o.note, u.name, o.created_at`

const orderFrom = `FROM bakery_orders o
	JOIN branches b ON b.id = o.branch_id
	JOIN products p ON p.id = o.product_id
	LEFT JOIN users u ON u.id = o.requested_by`

func scanOrder(row pgx.Row) (Order, error) {
	var o Order
	err := row.Scan(&o.ID, &o.BranchID, &o.BranchName, &o.ProductID, &o.ProductName,
		&o.QuantityOrdered, &o.QuantityShipped, &o.Status, &o.Note, &o.RequestedByName, &o.CreatedAt)
	return o, err
}

// branchInTenant indica si la sucursal es del negocio (uuid mal formado/ajeno => false).
func (s *store) branchInTenant(ctx context.Context, tenantID, branchID string) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM branches WHERE id = $1 AND tenant_id = $2)`,
		branchID, tenantID).Scan(&ok)
	if isInvalidUUID(err) {
		return false, nil
	}
	return ok, err
}

// productBakeryActive indica si el producto es del negocio, está activo y es de
// repostería (fulfillment_type='bakery'). uuid mal formado/ajeno => false.
func (s *store) productBakeryActive(ctx context.Context, tenantID, productID string) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM products
		    WHERE id = $1 AND tenant_id = $2 AND status = 'active' AND fulfillment_type = 'bakery')`,
		productID, tenantID).Scan(&ok)
	if isInvalidUUID(err) {
		return false, nil
	}
	return ok, err
}

// ---- Pedidos ---------------------------------------------------------------

// createOrder inserta un pedido en estado 'pending' y devuelve el pedido con nombres
// resueltos. La validación de sucursal/producto la hace el service antes.
func (s *store) createOrder(ctx context.Context, tenantID, branchID, productID string, quantity int, note *string, requestedBy string) (Order, error) {
	var id string
	err := s.pool.QueryRow(ctx,
		`INSERT INTO bakery_orders (tenant_id, branch_id, product_id, quantity_ordered, note, requested_by)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 RETURNING id::text`,
		tenantID, branchID, productID, quantity, note, requestedBy).Scan(&id)
	if err != nil {
		return Order{}, err
	}
	return s.getOrder(ctx, tenantID, id)
}

// getOrder devuelve un pedido del negocio con nombres resueltos. uuid mal formado/ajeno
// => ErrNotFound.
func (s *store) getOrder(ctx context.Context, tenantID, id string) (Order, error) {
	o, err := scanOrder(s.pool.QueryRow(ctx,
		`SELECT `+orderCols+` `+orderFrom+` WHERE o.id = $1 AND o.tenant_id = $2`, id, tenantID))
	switch {
	case errors.Is(err, pgx.ErrNoRows), isInvalidUUID(err):
		return Order{}, ErrNotFound
	case err != nil:
		return Order{}, err
	}
	return o, nil
}

// listOrders lista pedidos del negocio en orden FIFO (created_at ASC). branchID fuerza el
// scope de sucursal (nil = todas); statuses filtra por estado (vacío = todos); q filtra
// por nombre de postre (ILIKE).
func (s *store) listOrders(ctx context.Context, tenantID string, branchID *string, statuses []string, q string) ([]Order, error) {
	args := []any{tenantID}
	sql := `SELECT ` + orderCols + ` ` + orderFrom + ` WHERE o.tenant_id = $1`
	if branchID != nil {
		args = append(args, *branchID)
		sql += ` AND o.branch_id = $` + strconv.Itoa(len(args))
	}
	if len(statuses) > 0 {
		args = append(args, statuses)
		sql += ` AND o.status = ANY($` + strconv.Itoa(len(args)) + `::text[])`
	}
	if q != "" {
		args = append(args, "%"+q+"%")
		sql += ` AND p.name ILIKE $` + strconv.Itoa(len(args))
	}
	sql += ` ORDER BY o.created_at ASC`

	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Order{}
	for rows.Next() {
		o, err := scanOrder(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// productionsForOrder devuelve las producciones de un pedido (detalle §5.3), ASC.
func (s *store) productionsForOrder(ctx context.Context, tenantID, orderID string) ([]Production, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT pr.id::text, pr.quantity_produced, u.name, pr.created_at
		   FROM bakery_productions pr
		   LEFT JOIN users u ON u.id = pr.created_by
		  WHERE pr.tenant_id = $1 AND pr.order_id = $2
		  ORDER BY pr.created_at ASC`, tenantID, orderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Production{}
	for rows.Next() {
		var p Production
		if err := rows.Scan(&p.ID, &p.QuantityProduced, &p.CreatedByName, &p.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// cancelOrder cancela un pedido bajo FOR UPDATE: solo si status='pending' y
// quantity_shipped=0 (F12). Cualquier otro estado => ErrInvalidState. uuid/ajeno =>
// ErrNotFound.
func (s *store) cancelOrder(ctx context.Context, tenantID, id string) (Order, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Order{}, err
	}
	defer tx.Rollback(ctx)

	var status string
	var shipped int
	err = tx.QueryRow(ctx,
		`SELECT status, quantity_shipped FROM bakery_orders WHERE id = $1 AND tenant_id = $2 FOR UPDATE`,
		id, tenantID).Scan(&status, &shipped)
	switch {
	case errors.Is(err, pgx.ErrNoRows), isInvalidUUID(err):
		return Order{}, ErrNotFound
	case err != nil:
		return Order{}, err
	}
	if status != "pending" || shipped != 0 {
		return Order{}, ErrInvalidState
	}
	if _, err := tx.Exec(ctx,
		`UPDATE bakery_orders SET status = 'cancelled', updated_at = now() WHERE id = $1 AND tenant_id = $2`,
		id, tenantID); err != nil {
		return Order{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Order{}, err
	}
	return s.getOrder(ctx, tenantID, id)
}

// receiveOrder marca un pedido como recibido bajo FOR UPDATE: solo desde 'shipped' (F14).
// NO mueve stock (D5). Otro estado => ErrInvalidState.
func (s *store) receiveOrder(ctx context.Context, tenantID, id string) (Order, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Order{}, err
	}
	defer tx.Rollback(ctx)

	var status string
	err = tx.QueryRow(ctx,
		`SELECT status FROM bakery_orders WHERE id = $1 AND tenant_id = $2 FOR UPDATE`,
		id, tenantID).Scan(&status)
	switch {
	case errors.Is(err, pgx.ErrNoRows), isInvalidUUID(err):
		return Order{}, ErrNotFound
	case err != nil:
		return Order{}, err
	}
	if status != "shipped" {
		return Order{}, ErrInvalidState
	}
	if _, err := tx.Exec(ctx,
		`UPDATE bakery_orders SET status = 'received', updated_at = now() WHERE id = $1 AND tenant_id = $2`,
		id, tenantID); err != nil {
		return Order{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Order{}, err
	}
	return s.getOrder(ctx, tenantID, id)
}

// ---- Transacción central de producción (§4, ADR-010 D3) --------------------

// produce ejecuta el doble efecto de una producción en UNA transacción (réplica de
// warehouse.insertDispatch). SELECT ... FOR UPDATE sobre el pedido serializa producciones
// concurrentes (R3). Descuenta insumos del almacén central (ORDER BY supply_id,
// anti-deadlock) y acredita el postre a la sucursal del pedido, ambos con su ledger + cache
// en la misma tx (invariante stock == SUM(movimientos)). No bloqueante: stock puede quedar
// negativo (D-B). Devuelve el pedido actualizado y la producción registrada.
func (s *store) produce(ctx context.Context, tenantID, orderID string, quantity int, createdBy string) (Order, Production, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Order{}, Production{}, err
	}
	defer tx.Rollback(ctx)

	// 1. Bloqueo del pedido (serializa producciones concurrentes).
	var branchID, productID, status string
	var qtyOrdered, qtyShipped int
	err = tx.QueryRow(ctx,
		`SELECT branch_id::text, product_id::text, quantity_ordered, quantity_shipped, status
		   FROM bakery_orders WHERE id = $1 AND tenant_id = $2 FOR UPDATE`,
		orderID, tenantID).Scan(&branchID, &productID, &qtyOrdered, &qtyShipped, &status)
	switch {
	case errors.Is(err, pgx.ErrNoRows), isInvalidUUID(err):
		return Order{}, Production{}, ErrNotFound
	case err != nil:
		return Order{}, Production{}, err
	}

	// 2. Validaciones bajo lock.
	if status != "pending" && status != "in_production" {
		return Order{}, Production{}, ErrInvalidState
	}
	// Revalidar (defensivo) que el producto sigue siendo bakery + activo: el bloqueo D-C lo
	// previene, pero se revalida bajo lock.
	var ft, pstatus string
	err = tx.QueryRow(ctx,
		`SELECT fulfillment_type, status FROM products WHERE id = $1 AND tenant_id = $2`,
		productID, tenantID).Scan(&ft, &pstatus)
	if err != nil {
		return Order{}, Production{}, err
	}
	if ft != "bakery" || pstatus != "active" {
		return Order{}, Production{}, ErrInvalidState
	}

	// 3. Registrar la producción (auditoría; snapshot de producto/sucursal).
	var prodID string
	var prodAt time.Time
	err = tx.QueryRow(ctx,
		`INSERT INTO bakery_productions (tenant_id, order_id, product_id, branch_id, quantity_produced, created_by)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 RETURNING id::text, created_at`,
		tenantID, orderID, productID, branchID, quantity, createdBy).Scan(&prodID, &prodAt)
	if err != nil {
		return Order{}, Production{}, err
	}

	// 4/5. Consumo de insumos del almacén central (receta × cantidad). Set-based con
	// ORDER BY supply_id (orden de bloqueo determinista, anti-deadlock). Sin receta =>
	// cero filas (no-op natural). type='production', branch_id NULL, ligado a la producción.
	if _, err := tx.Exec(ctx,
		`WITH consumo AS (
		     SELECT ps.supply_id, (ps.quantity_base * $3)::int AS total
		       FROM product_supplies ps
		      WHERE ps.product_id = $2 AND ps.tenant_id = $1
		 ),
		 mov AS (
		     INSERT INTO warehouse_movements
		         (tenant_id, supply_id, type, quantity_base, branch_id, bakery_production_id, created_by)
		     SELECT $1, supply_id, 'production', -total, NULL, $4, $5
		       FROM consumo
		     RETURNING supply_id, quantity_base
		 )
		 INSERT INTO warehouse_stock (tenant_id, supply_id, stock_base)
		 SELECT $1, supply_id, quantity_base
		   FROM mov
		  ORDER BY supply_id
		 ON CONFLICT (supply_id) DO UPDATE
		    SET stock_base = warehouse_stock.stock_base + EXCLUDED.stock_base,
		        updated_at = now()`,
		tenantID, productID, quantity, prodID, createdBy); err != nil {
		return Order{}, Production{}, err
	}

	// 6. Acreditar el postre a la sucursal del pedido (ledger firmado + cache).
	if _, err := tx.Exec(ctx,
		`INSERT INTO product_stock_movements
		     (tenant_id, product_id, branch_id, type, quantity, bakery_production_id, created_by)
		 VALUES ($1, $2, $3, 'production_in', $4, $5, $6)`,
		tenantID, productID, branchID, quantity, prodID, createdBy); err != nil {
		return Order{}, Production{}, err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO product_branch_stock (tenant_id, product_id, branch_id, stock_qty)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (product_id, branch_id) DO UPDATE
		    SET stock_qty = product_branch_stock.stock_qty + EXCLUDED.stock_qty,
		        updated_at = now()`,
		tenantID, productID, branchID, quantity); err != nil {
		return Order{}, Production{}, err
	}

	// 7. Avanzar el pedido: quantity_shipped acumulado; 'shipped' al alcanzar lo pedido.
	if _, err := tx.Exec(ctx,
		`UPDATE bakery_orders
		    SET quantity_shipped = quantity_shipped + $3,
		        status = CASE WHEN quantity_shipped + $3 >= quantity_ordered THEN 'shipped'
		                      ELSE 'in_production' END,
		        updated_at = now()
		  WHERE id = $1 AND tenant_id = $2`,
		orderID, tenantID, quantity); err != nil {
		return Order{}, Production{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return Order{}, Production{}, err
	}

	order, err := s.getOrder(ctx, tenantID, orderID)
	if err != nil {
		return Order{}, Production{}, err
	}
	return order, Production{ID: prodID, QuantityProduced: quantity, CreatedAt: prodAt}, nil
}

// ---- Stock de postres (§3.4 D-D) -------------------------------------------

// listStock devuelve una fila por (producto bakery, sucursal) con stock. branchID acota a
// una sucursal (nil = todas); q filtra por nombre de postre. Orden: productName, branchName.
func (s *store) listStock(ctx context.Context, tenantID string, branchID *string, q string) ([]StockItem, error) {
	args := []any{tenantID}
	sql := `SELECT p.id::text, p.name, b.id::text, b.name, pbs.stock_qty
		      FROM product_branch_stock pbs
		      JOIN products p ON p.id = pbs.product_id
		      JOIN branches b ON b.id = pbs.branch_id
		     WHERE pbs.tenant_id = $1 AND p.fulfillment_type = 'bakery'`
	if branchID != nil {
		args = append(args, *branchID)
		sql += ` AND pbs.branch_id = $` + strconv.Itoa(len(args))
	}
	if q != "" {
		args = append(args, "%"+q+"%")
		sql += ` AND p.name ILIKE $` + strconv.Itoa(len(args))
	}
	sql += ` ORDER BY p.name, b.name`

	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []StockItem{}
	for rows.Next() {
		var it StockItem
		if err := rows.Scan(&it.ProductID, &it.ProductName, &it.BranchID, &it.BranchName, &it.StockQty); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// ---- Auditoría de producciones (§5.8) --------------------------------------

// listProductions lista producciones del negocio (DESC, LIMIT 100) con filtros
// opcionales de rango, sucursal y producto.
func (s *store) listProductions(ctx context.Context, tenantID string, from, to *time.Time, branchID, productID *string) ([]ProductionAudit, error) {
	args := []any{tenantID}
	sql := `SELECT pr.id::text, pr.order_id::text, p.name, b.name, pr.quantity_produced, u.name, pr.created_at
		      FROM bakery_productions pr
		      JOIN products p ON p.id = pr.product_id
		      JOIN branches b ON b.id = pr.branch_id
		      LEFT JOIN users u ON u.id = pr.created_by
		     WHERE pr.tenant_id = $1`
	if from != nil {
		args = append(args, *from)
		sql += ` AND pr.created_at >= $` + strconv.Itoa(len(args))
	}
	if to != nil {
		args = append(args, *to)
		sql += ` AND pr.created_at < $` + strconv.Itoa(len(args))
	}
	if branchID != nil {
		args = append(args, *branchID)
		sql += ` AND pr.branch_id = $` + strconv.Itoa(len(args))
	}
	if productID != nil {
		args = append(args, *productID)
		sql += ` AND pr.product_id = $` + strconv.Itoa(len(args))
	}
	sql += ` ORDER BY pr.created_at DESC LIMIT 100`

	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		// uuid mal formado en un filtro => sin resultados (defensivo).
		if isInvalidUUID(err) {
			return []ProductionAudit{}, nil
		}
		return nil, err
	}
	defer rows.Close()

	out := []ProductionAudit{}
	for rows.Next() {
		var it ProductionAudit
		if err := rows.Scan(&it.ID, &it.OrderID, &it.ProductName, &it.BranchName, &it.QuantityProduced, &it.CreatedByName, &it.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// parseStatuses parte un CSV de estados y descarta valores no soportados.
func parseStatuses(csv string) []string {
	if strings.TrimSpace(csv) == "" {
		return nil
	}
	valid := map[string]bool{"pending": true, "in_production": true, "shipped": true, "received": true, "cancelled": true}
	out := []string{}
	seen := map[string]bool{}
	for _, s := range strings.Split(csv, ",") {
		v := strings.TrimSpace(s)
		if v != "" && valid[v] && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}
