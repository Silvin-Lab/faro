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

// productBakeryName devuelve el nombre del producto si es del negocio, activo y de
// repostería; "" si no cumple (o uuid mal formado/ajeno).
func (s *store) productBakeryName(ctx context.Context, tenantID, productID string) (string, error) {
	var name string
	err := s.pool.QueryRow(ctx,
		`SELECT name FROM products
		  WHERE id = $1 AND tenant_id = $2 AND status = 'active' AND fulfillment_type = 'bakery'`,
		productID, tenantID).Scan(&name)
	switch {
	case errors.Is(err, pgx.ErrNoRows), isInvalidUUID(err):
		return "", nil
	case err != nil:
		return "", err
	}
	return name, nil
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

// ---- Merma de postre (§merma de repostería) --------------------------------

// insertWaste registra una merma de postre en UNA transacción (calca
// warehouse.insertWasteBranch pero sobre product_branch_stock/product_stock_movements):
// revalida que el producto siga siendo bakery + activo bajo la misma tx, inserta el
// movimiento firmado (type='waste', quantity=-qty) y actualiza el cache de la sucursal
// (upsert lazy). Devuelve el movimiento y el nuevo stock de la sucursal. qty > 0 (el
// service lo valida); se guarda como negativo.
func (s *store) insertWaste(ctx context.Context, tenantID, productID, branchID string, qty int, reason, createdBy string) (WasteMovement, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return WasteMovement{}, err
	}
	defer tx.Rollback(ctx)

	var ft, pstatus, pname string
	err = tx.QueryRow(ctx,
		`SELECT fulfillment_type, status, name FROM products WHERE id = $1 AND tenant_id = $2`,
		productID, tenantID).Scan(&ft, &pstatus, &pname)
	switch {
	case errors.Is(err, pgx.ErrNoRows), isInvalidUUID(err):
		return WasteMovement{}, ErrInvalidProduct
	case err != nil:
		return WasteMovement{}, err
	}
	if ft != "bakery" || pstatus != "active" {
		return WasteMovement{}, ErrInvalidProduct
	}

	var m WasteMovement
	err = tx.QueryRow(ctx,
		`INSERT INTO product_stock_movements
		     (tenant_id, product_id, branch_id, type, quantity, reason, created_by)
		 VALUES ($1, $2, $3, 'waste', $4, $5, $6)
		 RETURNING id::text, created_at`,
		tenantID, productID, branchID, -qty, reason, createdBy).Scan(&m.ID, &m.CreatedAt)
	if err != nil {
		return WasteMovement{}, err
	}

	var stockQty int
	err = tx.QueryRow(ctx,
		`INSERT INTO product_branch_stock (tenant_id, product_id, branch_id, stock_qty)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (product_id, branch_id) DO UPDATE
		    SET stock_qty = product_branch_stock.stock_qty + EXCLUDED.stock_qty,
		        updated_at = now()
		 RETURNING stock_qty`,
		tenantID, productID, branchID, -qty).Scan(&stockQty)
	if err != nil {
		return WasteMovement{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return WasteMovement{}, err
	}

	r := reason
	m.ProductID = productID
	m.ProductName = pname
	m.BranchID = branchID
	m.Quantity = -qty
	m.Reason = &r
	m.StockQty = stockQty
	return m, nil
}

// listWaste lista las mermas de postre del negocio (DESC, LIMIT 100) con filtros
// opcionales de rango y sucursal.
func (s *store) listWaste(ctx context.Context, tenantID string, from, to *time.Time, branchID *string) ([]WasteAudit, error) {
	args := []any{tenantID}
	sql := `SELECT m.id::text, m.product_id::text, p.name, m.branch_id::text, b.name,
		           m.quantity, m.reason, u.name, m.created_at
		      FROM product_stock_movements m
		      JOIN products p ON p.id = m.product_id
		      JOIN branches b ON b.id = m.branch_id
		      LEFT JOIN users u ON u.id = m.created_by
		     WHERE m.tenant_id = $1 AND m.type = 'waste'`
	if from != nil {
		args = append(args, *from)
		sql += ` AND m.created_at >= $` + strconv.Itoa(len(args))
	}
	if to != nil {
		args = append(args, *to)
		sql += ` AND m.created_at < $` + strconv.Itoa(len(args))
	}
	if branchID != nil {
		args = append(args, *branchID)
		sql += ` AND m.branch_id = $` + strconv.Itoa(len(args))
	}
	sql += ` ORDER BY m.created_at DESC LIMIT 100`

	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		if isInvalidUUID(err) {
			return []WasteAudit{}, nil
		}
		return nil, err
	}
	defer rows.Close()

	out := []WasteAudit{}
	for rows.Next() {
		var it WasteAudit
		if err := rows.Scan(&it.ID, &it.ProductID, &it.ProductName, &it.BranchID, &it.BranchName,
			&it.Quantity, &it.Reason, &it.CreatedByName, &it.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// ---- Conteo de cierre de postres (reconciliación) --------------------------

// Motivos fijos de la reconciliación automática del conteo de cierre.
const (
	countWasteReason  = "Merma automática por conteo de cierre"
	countAdjustReason = "Ajuste por conteo de cierre (sobrante)"
)

// createCount ejecuta el conteo de cierre en UNA transacción: por cada línea (en orden
// ascendente de product_id, ya ordenado por el service para evitar deadlocks, mismo
// criterio que produce()) bloquea el cache de la sucursal con SELECT ... FOR UPDATE,
// calcula expected = stock actual, diff = contado - expected; si diff<0 registra una
// merma (type='waste'), si diff>0 un ajuste (type='adjustment'), si diff=0 no mueve nada;
// y deja el cache en el valor contado. Inserta el header y las líneas. Devuelve el detalle
// con el tipo de movimiento por línea. lines debe venir ordenado por ProductID ASC y con
// productos ya validados (bakery + activos, sin duplicados).
func (s *store) createCount(ctx context.Context, tenantID, branchID string, note *string, lines []countLine, createdBy string) (CountDetail, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return CountDetail{}, err
	}
	defer tx.Rollback(ctx)

	var d CountDetail
	err = tx.QueryRow(ctx,
		`INSERT INTO bakery_counts (tenant_id, branch_id, note, created_by)
		 VALUES ($1, $2, $3, $4)
		 RETURNING id::text, created_at`,
		tenantID, branchID, note, createdBy).Scan(&d.Count.ID, &d.Count.CreatedAt)
	if err != nil {
		return CountDetail{}, err
	}

	d.Items = []CountItem{}
	for _, ln := range lines {
		// Bloqueo del cache de la sucursal para este producto (lazy: puede no existir).
		// LIMITACIÓN CONOCIDA (aceptada MVP): si la fila aún NO existe (producto sin
		// movimientos previos en esta sucursal), FOR UPDATE no bloquea nada y dos conteos
		// concurrentes del mismo (producto, sucursal) podrían intercalar su INSERT
		// ON CONFLICT. Cuando la fila existe (caso normal: hubo producción antes) el lock sí
		// serializa. El escenario (dos cierres físicos simultáneos del mismo postre SIN
		// historial en esa sucursal) es muy poco probable en la operación de Vanta.
		// TODO(v2): materializar la fila lazy (INSERT ... ON CONFLICT DO NOTHING) antes del
		// FOR UPDATE para cerrar la ventana.
		var expected int
		err = tx.QueryRow(ctx,
			`SELECT stock_qty FROM product_branch_stock
			  WHERE product_id = $1 AND branch_id = $2 AND tenant_id = $3 FOR UPDATE`,
			ln.ProductID, branchID, tenantID).Scan(&expected)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return CountDetail{}, err
		}
		// pgx.ErrNoRows => sin fila: expected queda en 0.

		diff := ln.CountedQty - expected
		var movementID *string
		var movementType *string
		if diff != 0 {
			typ := "adjustment"
			reason := countAdjustReason
			if diff < 0 {
				typ = "waste"
				reason = countWasteReason
			}
			var mid string
			err = tx.QueryRow(ctx,
				`INSERT INTO product_stock_movements
				     (tenant_id, product_id, branch_id, type, quantity, reason, bakery_count_id, created_by)
				 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
				 RETURNING id::text`,
				tenantID, ln.ProductID, branchID, typ, diff, reason, d.Count.ID, createdBy).Scan(&mid)
			if err != nil {
				return CountDetail{}, err
			}
			// Deja el cache en el valor contado (expected + diff == countedQty).
			if _, err := tx.Exec(ctx,
				`INSERT INTO product_branch_stock (tenant_id, product_id, branch_id, stock_qty)
				 VALUES ($1, $2, $3, $4)
				 ON CONFLICT (product_id, branch_id) DO UPDATE
				    SET stock_qty = product_branch_stock.stock_qty + EXCLUDED.stock_qty,
				        updated_at = now()`,
				tenantID, ln.ProductID, branchID, diff); err != nil {
				return CountDetail{}, err
			}
			movementID = &mid
			t := typ
			movementType = &t
		}

		var itemID string
		err = tx.QueryRow(ctx,
			`INSERT INTO bakery_count_items
			     (tenant_id, count_id, product_id, expected_qty, counted_qty, diff_qty, movement_id)
			 VALUES ($1, $2, $3, $4, $5, $6, $7)
			 RETURNING id::text`,
			tenantID, d.Count.ID, ln.ProductID, expected, ln.CountedQty, diff, movementID).Scan(&itemID)
		if err != nil {
			return CountDetail{}, err
		}
		d.Items = append(d.Items, CountItem{
			ID: itemID, ProductID: ln.ProductID, ProductName: ln.ProductName,
			ExpectedQty: expected, CountedQty: ln.CountedQty, DiffQty: diff,
			MovementID: movementID, MovementType: movementType,
		})
	}

	if err := tx.Commit(ctx); err != nil {
		return CountDetail{}, err
	}

	// Completar el header con nombres resueltos para la respuesta.
	head, err := s.getCount(ctx, tenantID, d.Count.ID)
	if err != nil {
		return CountDetail{}, err
	}
	d.Count = head.Count
	return d, nil
}

// getCount devuelve un conteo (header + líneas con nombres resueltos y movementType).
// uuid mal formado/ajeno => ErrNotFound.
func (s *store) getCount(ctx context.Context, tenantID, id string) (CountDetail, error) {
	var d CountDetail
	err := s.pool.QueryRow(ctx,
		`SELECT c.id::text, c.branch_id::text, b.name, c.note, u.name, c.created_at
		   FROM bakery_counts c
		   JOIN branches b ON b.id = c.branch_id
		   LEFT JOIN users u ON u.id = c.created_by
		  WHERE c.id = $1 AND c.tenant_id = $2`, id, tenantID).
		Scan(&d.Count.ID, &d.Count.BranchID, &d.Count.BranchName, &d.Count.Note, &d.Count.CreatedByName, &d.Count.CreatedAt)
	switch {
	case errors.Is(err, pgx.ErrNoRows), isInvalidUUID(err):
		return CountDetail{}, ErrNotFound
	case err != nil:
		return CountDetail{}, err
	}

	rows, err := s.pool.Query(ctx,
		`SELECT ci.id::text, ci.product_id::text, p.name, ci.expected_qty, ci.counted_qty,
		        ci.diff_qty, ci.movement_id::text, m.type
		   FROM bakery_count_items ci
		   JOIN products p ON p.id = ci.product_id
		   LEFT JOIN product_stock_movements m ON m.id = ci.movement_id
		  WHERE ci.count_id = $1 AND ci.tenant_id = $2
		  ORDER BY p.name`, id, tenantID)
	if err != nil {
		return CountDetail{}, err
	}
	defer rows.Close()

	d.Items = []CountItem{}
	for rows.Next() {
		var it CountItem
		if err := rows.Scan(&it.ID, &it.ProductID, &it.ProductName, &it.ExpectedQty, &it.CountedQty,
			&it.DiffQty, &it.MovementID, &it.MovementType); err != nil {
			return CountDetail{}, err
		}
		d.Items = append(d.Items, it)
	}
	return d, rows.Err()
}

// listCounts lista los encabezados de conteo del negocio (DESC, LIMIT 100) con filtros
// opcionales de sucursal y rango.
func (s *store) listCounts(ctx context.Context, tenantID string, branchID *string, from, to *time.Time) ([]Count, error) {
	args := []any{tenantID}
	sql := `SELECT c.id::text, c.branch_id::text, b.name, c.note, u.name, c.created_at
		      FROM bakery_counts c
		      JOIN branches b ON b.id = c.branch_id
		      LEFT JOIN users u ON u.id = c.created_by
		     WHERE c.tenant_id = $1`
	if branchID != nil {
		args = append(args, *branchID)
		sql += ` AND c.branch_id = $` + strconv.Itoa(len(args))
	}
	if from != nil {
		args = append(args, *from)
		sql += ` AND c.created_at >= $` + strconv.Itoa(len(args))
	}
	if to != nil {
		args = append(args, *to)
		sql += ` AND c.created_at < $` + strconv.Itoa(len(args))
	}
	sql += ` ORDER BY c.created_at DESC LIMIT 100`

	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		if isInvalidUUID(err) {
			return []Count{}, nil
		}
		return nil, err
	}
	defer rows.Close()

	out := []Count{}
	for rows.Next() {
		var c Count
		if err := rows.Scan(&c.ID, &c.BranchID, &c.BranchName, &c.Note, &c.CreatedByName, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
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
