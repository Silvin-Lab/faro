package warehouse

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"faro/internal/supplies"
)

var (
	ErrNotFound        = errors.New("not found")
	ErrNameTaken       = errors.New("name taken")
	ErrInvalidSupply   = errors.New("invalid supply")
	ErrInvalidSupplier = errors.New("invalid supplier")
	ErrInvalidBranch   = errors.New("invalid branch")
	ErrValidation      = errors.New("validation")
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

// ---- Existencia / pertenencia al tenant ------------------------------------

// supplyInfo devuelve (package_content, ok) si el insumo pertenece al tenant. uuid
// mal formado / ajeno => ok=false.
func (s *store) supplyInfo(ctx context.Context, tenantID, supplyID string) (int, bool, error) {
	var pc int
	err := s.pool.QueryRow(ctx,
		`SELECT package_content FROM supplies WHERE id = $1 AND tenant_id = $2`,
		supplyID, tenantID).Scan(&pc)
	switch {
	case errors.Is(err, pgx.ErrNoRows), pgCode(err, "22P02"):
		return 0, false, nil
	case err != nil:
		return 0, false, err
	}
	return pc, true, nil
}

func (s *store) supplierInTenant(ctx context.Context, tenantID, supplierID string) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM suppliers WHERE id = $1 AND tenant_id = $2)`,
		supplierID, tenantID).Scan(&ok)
	if pgCode(err, "22P02") {
		return false, nil
	}
	return ok, err
}

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

// ---- Proveedores -----------------------------------------------------------

func (s *store) createSupplier(ctx context.Context, tenantID, name string, address, email, phone *string) (Supplier, error) {
	var sp Supplier
	err := s.pool.QueryRow(ctx,
		`INSERT INTO suppliers (tenant_id, name, address, email, phone)
		 VALUES ($1, $2, $3, $4, $5)
		 RETURNING id::text, tenant_id::text, name, address, email, phone, status, created_at`,
		tenantID, name, address, email, phone).
		Scan(&sp.ID, &sp.TenantID, &sp.Name, &sp.Address, &sp.Email, &sp.Phone, &sp.Status, &sp.CreatedAt)
	if pgCode(err, "23505") {
		return Supplier{}, ErrNameTaken
	}
	return sp, err
}

func (s *store) listSuppliers(ctx context.Context, tenantID string, status *string) ([]Supplier, error) {
	args := []any{tenantID}
	q := `SELECT id::text, tenant_id::text, name, address, email, phone, status, created_at
	        FROM suppliers WHERE tenant_id = $1`
	if status != nil {
		args = append(args, *status)
		q += ` AND status = $2`
	}
	q += ` ORDER BY name`
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Supplier{}
	for rows.Next() {
		var sp Supplier
		if err := rows.Scan(&sp.ID, &sp.TenantID, &sp.Name, &sp.Address, &sp.Email, &sp.Phone, &sp.Status, &sp.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, sp)
	}
	return out, rows.Err()
}

func (s *store) updateSupplier(ctx context.Context, tenantID, id string, name, address, email, phone, status *string) (Supplier, error) {
	var sp Supplier
	err := s.pool.QueryRow(ctx,
		`UPDATE suppliers
		    SET name    = COALESCE($3, name),
		        address = COALESCE($4, address),
		        email   = COALESCE($5, email),
		        phone   = COALESCE($6, phone),
		        status  = COALESCE($7, status)
		  WHERE id = $1 AND tenant_id = $2
		  RETURNING id::text, tenant_id::text, name, address, email, phone, status, created_at`,
		id, tenantID, name, address, email, phone, status).
		Scan(&sp.ID, &sp.TenantID, &sp.Name, &sp.Address, &sp.Email, &sp.Phone, &sp.Status, &sp.CreatedAt)
	switch {
	case errors.Is(err, pgx.ErrNoRows), pgCode(err, "22P02"):
		return Supplier{}, ErrNotFound
	case pgCode(err, "23505"):
		return Supplier{}, ErrNameTaken
	case err != nil:
		return Supplier{}, err
	}
	return sp, nil
}

// ---- Stock del almacén + mín/máx -------------------------------------------

// deriveStatus computa el badge de estado (regla del handoff §2.1): below_min si
// min definido y stock<=min; no_min si min null; si no ok.
func deriveStatus(stockBase int, min *int) string {
	if min == nil {
		return "no_min"
	}
	if stockBase <= *min {
		return "below_min"
	}
	return "ok"
}

// listStock devuelve todos los supplies del tenant (LEFT JOIN warehouse_stock), con
// stock/min/max y status derivado. Supply sin fila => stock 0, sin mín/máx.
func (s *store) listStock(ctx context.Context, tenantID string, status *string) ([]WarehouseStockItem, error) {
	args := []any{tenantID}
	q := `SELECT sp.id::text, sp.name, sp.base_unit, sp.package_name, sp.package_content,
	             sp.package_cost_cents, COALESCE(ws.stock_base, 0), ws.min_quantity, ws.max_quantity
	        FROM supplies sp
	        LEFT JOIN warehouse_stock ws ON ws.supply_id = sp.id
	       WHERE sp.tenant_id = $1`
	if status != nil {
		args = append(args, *status)
		q += ` AND sp.status = $2`
	}
	q += ` ORDER BY sp.name`
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []WarehouseStockItem{}
	for rows.Next() {
		var it WarehouseStockItem
		if err := rows.Scan(&it.SupplyID, &it.Name, &it.BaseUnit, &it.PackageName, &it.PackageContent,
			&it.PackageCostCents, &it.StockBase, &it.MinQuantity, &it.MaxQuantity); err != nil {
			return nil, err
		}
		it.Status = deriveStatus(it.StockBase, it.MinQuantity)
		out = append(out, it)
	}
	return out, rows.Err()
}

// stockItem devuelve un WarehouseStockItem para un supply del tenant (LEFT JOIN).
func (s *store) stockItem(ctx context.Context, q pgx.Row) (WarehouseStockItem, error) {
	var it WarehouseStockItem
	err := q.Scan(&it.SupplyID, &it.Name, &it.BaseUnit, &it.PackageName, &it.PackageContent,
		&it.PackageCostCents, &it.StockBase, &it.MinQuantity, &it.MaxQuantity)
	if err != nil {
		return WarehouseStockItem{}, err
	}
	it.Status = deriveStatus(it.StockBase, it.MinQuantity)
	return it, nil
}

// upsertMinMax aplica el cambio de mín/máx (presencia por MinMaxUpdate), validando
// max >= min con el estado combinado (persistido + cambios). Crea la fila lazy si no
// existe. Devuelve el item resultante con status derivado.
func (s *store) upsertMinMax(ctx context.Context, tenantID, supplyID string, in MinMaxUpdate) (WarehouseStockItem, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return WarehouseStockItem{}, err
	}
	defer tx.Rollback(ctx)

	var curMin, curMax *int
	err = tx.QueryRow(ctx,
		`SELECT min_quantity, max_quantity FROM warehouse_stock WHERE supply_id = $1 AND tenant_id = $2`,
		supplyID, tenantID).Scan(&curMin, &curMax)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return WarehouseStockItem{}, err
	}

	finalMin := curMin
	if in.SetMin {
		finalMin = in.Min
	}
	finalMax := curMax
	if in.SetMax {
		finalMax = in.Max
	}
	if finalMin != nil && finalMax != nil && *finalMax < *finalMin {
		return WarehouseStockItem{}, ErrValidation
	}

	if _, err := tx.Exec(ctx,
		`INSERT INTO warehouse_stock (tenant_id, supply_id, min_quantity, max_quantity)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (supply_id)
		 DO UPDATE SET min_quantity = EXCLUDED.min_quantity,
		               max_quantity = EXCLUDED.max_quantity,
		               updated_at = now()`,
		tenantID, supplyID, finalMin, finalMax); err != nil {
		return WarehouseStockItem{}, err
	}

	it, err := s.stockItem(ctx, tx.QueryRow(ctx,
		`SELECT sp.id::text, sp.name, sp.base_unit, sp.package_name, sp.package_content,
		        sp.package_cost_cents, COALESCE(ws.stock_base, 0), ws.min_quantity, ws.max_quantity
		   FROM supplies sp
		   LEFT JOIN warehouse_stock ws ON ws.supply_id = sp.id
		  WHERE sp.id = $1 AND sp.tenant_id = $2`, supplyID, tenantID))
	if err != nil {
		return WarehouseStockItem{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return WarehouseStockItem{}, err
	}
	return it, nil
}

func (s *store) toBuy(ctx context.Context, tenantID string) ([]ToBuyItem, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT sp.id::text, sp.name, sp.base_unit, sp.package_name, sp.package_content,
		        COALESCE(ws.stock_base, 0) AS stock, ws.min_quantity
		   FROM supplies sp
		   JOIN warehouse_stock ws ON ws.supply_id = sp.id
		  WHERE sp.tenant_id = $1
		    AND ws.min_quantity IS NOT NULL
		    AND COALESCE(ws.stock_base, 0) <= ws.min_quantity
		  ORDER BY sp.name`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []ToBuyItem{}
	for rows.Next() {
		var it ToBuyItem
		if err := rows.Scan(&it.SupplyID, &it.Name, &it.BaseUnit, &it.PackageName, &it.PackageContent,
			&it.StockBase, &it.MinQuantity); err != nil {
			return nil, err
		}
		it.Missing = it.MinQuantity - it.StockBase
		if it.Missing < 0 {
			it.Missing = 0
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// upsertWarehouseStock aplica delta (FIRMADO) al cache del almacén DENTRO de tx
// (ON CONFLICT (supply_id) DO UPDATE ... += delta). Análogo del helper de sucursal
// (supplies.UpsertBranchStock). Reutilizado por compra/salida/merma. No valida
// existencias: el stock puede quedar negativo (R4). Devuelve el nuevo stock_base.
func upsertWarehouseStock(ctx context.Context, tx pgx.Tx, tenantID, supplyID string, delta int) (int, error) {
	var stockBase int
	err := tx.QueryRow(ctx,
		`INSERT INTO warehouse_stock (tenant_id, supply_id, stock_base)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (supply_id)
		 DO UPDATE SET stock_base = warehouse_stock.stock_base + EXCLUDED.stock_base,
		               updated_at = now()
		 RETURNING stock_base`,
		tenantID, supplyID, delta).Scan(&stockBase)
	return stockBase, err
}

// ---- Compras (purchase) ----------------------------------------------------

// insertPurchase escribe el movimiento purchase (+) y actualiza el cache del almacén
// en UNA transacción. quantityBase = packages * package_content (ya calculado).
// createdAt nil => now() (§4.4). Devuelve el movimiento y el nuevo stock del almacén.
func (s *store) insertPurchase(ctx context.Context, tenantID, supplyID, supplierID string, packages, unitCostCents, quantityBase int, createdBy string, createdAt *time.Time) (WarehouseMovement, int, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return WarehouseMovement{}, 0, err
	}
	defer tx.Rollback(ctx)

	var m WarehouseMovement
	err = tx.QueryRow(ctx,
		`INSERT INTO warehouse_movements
		     (tenant_id, supply_id, type, quantity_base, supplier_id, packages, unit_cost_cents, created_by, created_at)
		 VALUES ($1, $2, 'purchase', $3, $4, $5, $6, $7, COALESCE($8, now()))
		 RETURNING id::text, tenant_id::text, supply_id::text, type, quantity_base,
		           branch_id::text, supplier_id::text, packages, unit_cost_cents, reason, created_by::text, created_at`,
		tenantID, supplyID, quantityBase, supplierID, packages, unitCostCents, createdBy, createdAt).
		Scan(&m.ID, &m.TenantID, &m.SupplyID, &m.Type, &m.QuantityBase, &m.BranchID, &m.SupplierID,
			&m.Packages, &m.UnitCostCents, &m.Reason, &m.CreatedBy, &m.CreatedAt)
	if err != nil {
		return WarehouseMovement{}, 0, err
	}

	stockBase, err := upsertWarehouseStock(ctx, tx, tenantID, supplyID, quantityBase)
	if err != nil {
		return WarehouseMovement{}, 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return WarehouseMovement{}, 0, err
	}
	return m, stockBase, nil
}

func (s *store) listPurchases(ctx context.Context, tenantID string, from, to *time.Time) ([]PurchaseItem, error) {
	args := []any{tenantID}
	q := `SELECT m.id::text, m.supply_id::text, sp.name, m.supplier_id::text, sup.name,
	             m.packages, m.unit_cost_cents, m.quantity_base, u.name, m.created_at
	        FROM warehouse_movements m
	        JOIN supplies sp ON sp.id = m.supply_id
	        LEFT JOIN suppliers sup ON sup.id = m.supplier_id
	        LEFT JOIN users u ON u.id = m.created_by
	       WHERE m.tenant_id = $1 AND m.type = 'purchase'`
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
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []PurchaseItem{}
	for rows.Next() {
		var it PurchaseItem
		if err := rows.Scan(&it.ID, &it.SupplyID, &it.SupplyName, &it.SupplierID, &it.SupplierName,
			&it.Packages, &it.UnitCostCents, &it.QuantityBase, &it.CreatedByName, &it.CreatedAt); err != nil {
			return nil, err
		}
		if it.Packages != nil && it.UnitCostCents != nil {
			total := *it.Packages * *it.UnitCostCents
			it.TotalCents = &total
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// ---- Ajuste manual (adjustment) --------------------------------------------

// insertAdjustment FIJA la existencia del almacén de un insumo al total deseado
// (newQuantity, NO un delta): calcula la diferencia firmada contra el stock actual y,
// en UNA transacción, inserta un movimiento 'adjustment' con esa diferencia y
// actualiza el cache. Respeta el invariante stock_base == SUM(quantity_base). Si el
// valor deseado ya es el actual, es un NO-OP: no inserta movimiento (respeta
// CHECK(quantity_base <> 0)) y devuelve movimiento nil con el stock actual.
//
// Lee (y bloquea) la fila del cache con FOR UPDATE para serializar ajustes
// concurrentes del mismo insumo (mismo orden de bloqueo que el resto: warehouse_stock
// primero). Fila lazy (sin fila) => stock actual 0.
func (s *store) insertAdjustment(ctx context.Context, tenantID, supplyID string, newQuantity int, createdBy string, createdAt *time.Time) (*WarehouseMovement, int, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, 0, err
	}
	defer tx.Rollback(ctx)

	var current int
	err = tx.QueryRow(ctx,
		`SELECT stock_base FROM warehouse_stock WHERE supply_id = $1 AND tenant_id = $2 FOR UPDATE`,
		supplyID, tenantID).Scan(&current)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, 0, err
	}
	// pgx.ErrNoRows => fila lazy inexistente: current queda en 0.

	delta := newQuantity - current
	if delta == 0 {
		// No-op: ya está en el valor deseado. Se commitea para liberar el lock.
		if err := tx.Commit(ctx); err != nil {
			return nil, 0, err
		}
		return nil, current, nil
	}

	var m WarehouseMovement
	err = tx.QueryRow(ctx,
		`INSERT INTO warehouse_movements
		     (tenant_id, supply_id, type, quantity_base, created_by, created_at)
		 VALUES ($1, $2, 'adjustment', $3, $4, COALESCE($5, now()))
		 RETURNING id::text, tenant_id::text, supply_id::text, type, quantity_base,
		           branch_id::text, supplier_id::text, packages, unit_cost_cents, reason, created_by::text, created_at`,
		tenantID, supplyID, delta, createdBy, createdAt).
		Scan(&m.ID, &m.TenantID, &m.SupplyID, &m.Type, &m.QuantityBase, &m.BranchID, &m.SupplierID,
			&m.Packages, &m.UnitCostCents, &m.Reason, &m.CreatedBy, &m.CreatedAt)
	if err != nil {
		return nil, 0, err
	}

	stockBase, err := upsertWarehouseStock(ctx, tx, tenantID, supplyID, delta)
	if err != nil {
		return nil, 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, 0, err
	}
	return &m, stockBase, nil
}

// ---- Salidas (dispatch) — núcleo de R1/R2 (ADR-008) ------------------------

// insertDispatch ejecuta los 4 efectos de una salida en UNA transacción (tech-spec
// §3.2), con orden de bloqueo determinista: SIEMPRE se toca warehouse_stock antes
// que supply_branch_stock (anti-deadlock entre dispatches concurrentes del mismo
// supply/sucursal). El transfer de sucursal HEREDA el created_at del dispatch.
// Reutiliza el upsert de sucursal de supplies (UpsertBranchStock).
func (s *store) insertDispatch(ctx context.Context, tenantID, supplyID, branchID string, qty int, createdBy string, createdAt *time.Time) (WarehouseMovement, int, int, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return WarehouseMovement{}, 0, 0, err
	}
	defer tx.Rollback(ctx)

	// 1. warehouse_movements (dispatch, −qty, branch destino).
	var m WarehouseMovement
	err = tx.QueryRow(ctx,
		`INSERT INTO warehouse_movements
		     (tenant_id, supply_id, type, quantity_base, branch_id, created_by, created_at)
		 VALUES ($1, $2, 'dispatch', $3, $4, $5, COALESCE($6, now()))
		 RETURNING id::text, tenant_id::text, supply_id::text, type, quantity_base,
		           branch_id::text, supplier_id::text, packages, unit_cost_cents, reason, created_by::text, created_at`,
		tenantID, supplyID, -qty, branchID, createdBy, createdAt).
		Scan(&m.ID, &m.TenantID, &m.SupplyID, &m.Type, &m.QuantityBase, &m.BranchID, &m.SupplierID,
			&m.Packages, &m.UnitCostCents, &m.Reason, &m.CreatedBy, &m.CreatedAt)
	if err != nil {
		return WarehouseMovement{}, 0, 0, err
	}

	// 2. warehouse_stock −= qty (se bloquea SIEMPRE antes que supply_branch_stock).
	whStock, err := upsertWarehouseStock(ctx, tx, tenantID, supplyID, -qty)
	if err != nil {
		return WarehouseMovement{}, 0, 0, err
	}

	// 3. supply_movements (transfer, +qty, branch destino, ligado al dispatch por FK).
	//    Hereda el created_at exacto del dispatch (m.CreatedAt).
	if _, err := tx.Exec(ctx,
		`INSERT INTO supply_movements
		     (tenant_id, supply_id, branch_id, type, quantity_base, warehouse_movement_id, created_by, created_at)
		 VALUES ($1, $2, $3, 'transfer', $4, $5, $6, $7)`,
		tenantID, supplyID, branchID, qty, m.ID, createdBy, m.CreatedAt); err != nil {
		return WarehouseMovement{}, 0, 0, err
	}

	// 4. supply_branch_stock += qty (helper reutilizado de supplies).
	branchStock, err := supplies.UpsertBranchStock(ctx, tx, tenantID, supplyID, branchID, qty)
	if err != nil {
		return WarehouseMovement{}, 0, 0, err
	}

	if err := tx.Commit(ctx); err != nil {
		return WarehouseMovement{}, 0, 0, err
	}
	return m, whStock, branchStock, nil
}

func (s *store) listDispatches(ctx context.Context, tenantID string, from, to *time.Time) ([]DispatchItem, error) {
	args := []any{tenantID}
	q := `SELECT m.id::text, m.supply_id::text, sp.name, m.branch_id::text, b.name,
	             m.quantity_base, u.name, m.created_at
	        FROM warehouse_movements m
	        JOIN supplies sp ON sp.id = m.supply_id
	        LEFT JOIN branches b ON b.id = m.branch_id
	        LEFT JOIN users u ON u.id = m.created_by
	       WHERE m.tenant_id = $1 AND m.type = 'dispatch'`
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
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []DispatchItem{}
	for rows.Next() {
		var it DispatchItem
		if err := rows.Scan(&it.ID, &it.SupplyID, &it.SupplyName, &it.BranchID, &it.BranchName,
			&it.QuantityBase, &it.CreatedByName, &it.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// ---- Mermas (waste) — bifurcación por ubicación (R3, §3.3) -----------------

// insertWasteWarehouse registra una merma EN EL ALMACÉN (sin sucursal): movimiento
// warehouse_movements(waste, −qty) + warehouse_stock −= qty, en una transacción.
func (s *store) insertWasteWarehouse(ctx context.Context, tenantID, supplyID string, qty int, reason, createdBy string, createdAt *time.Time) (WarehouseMovement, int, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return WarehouseMovement{}, 0, err
	}
	defer tx.Rollback(ctx)

	var m WarehouseMovement
	err = tx.QueryRow(ctx,
		`INSERT INTO warehouse_movements
		     (tenant_id, supply_id, type, quantity_base, reason, created_by, created_at)
		 VALUES ($1, $2, 'waste', $3, $4, $5, COALESCE($6, now()))
		 RETURNING id::text, tenant_id::text, supply_id::text, type, quantity_base,
		           branch_id::text, supplier_id::text, packages, unit_cost_cents, reason, created_by::text, created_at`,
		tenantID, supplyID, -qty, reason, createdBy, createdAt).
		Scan(&m.ID, &m.TenantID, &m.SupplyID, &m.Type, &m.QuantityBase, &m.BranchID, &m.SupplierID,
			&m.Packages, &m.UnitCostCents, &m.Reason, &m.CreatedBy, &m.CreatedAt)
	if err != nil {
		return WarehouseMovement{}, 0, err
	}
	m.Origin = "warehouse"

	stockBase, err := upsertWarehouseStock(ctx, tx, tenantID, supplyID, -qty)
	if err != nil {
		return WarehouseMovement{}, 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return WarehouseMovement{}, 0, err
	}
	return m, stockBase, nil
}

// insertWasteBranch registra una merma EN UNA SUCURSAL (branch presente): movimiento
// supply_movements(waste, −qty) + supply_branch_stock −= qty, en una transacción.
// NO toca warehouse_stock (los bienes ya salieron del almacén vía dispatch; §3.3
// caso b). Reutiliza el upsert de sucursal de supplies.
func (s *store) insertWasteBranch(ctx context.Context, tenantID, supplyID, branchID string, qty int, reason, createdBy string, createdAt *time.Time) (WarehouseMovement, int, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return WarehouseMovement{}, 0, err
	}
	defer tx.Rollback(ctx)

	var m WarehouseMovement
	err = tx.QueryRow(ctx,
		`INSERT INTO supply_movements
		     (tenant_id, supply_id, branch_id, type, quantity_base, reason, created_by, created_at)
		 VALUES ($1, $2, $3, 'waste', $4, $5, $6, COALESCE($7, now()))
		 RETURNING id::text, tenant_id::text, supply_id::text, type, quantity_base,
		           branch_id::text, reason, created_by::text, created_at`,
		tenantID, supplyID, branchID, -qty, reason, createdBy, createdAt).
		Scan(&m.ID, &m.TenantID, &m.SupplyID, &m.Type, &m.QuantityBase, &m.BranchID,
			&m.Reason, &m.CreatedBy, &m.CreatedAt)
	if err != nil {
		return WarehouseMovement{}, 0, err
	}
	m.Origin = "branch"

	stockBase, err := supplies.UpsertBranchStock(ctx, tx, tenantID, supplyID, branchID, -qty)
	if err != nil {
		return WarehouseMovement{}, 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return WarehouseMovement{}, 0, err
	}
	return m, stockBase, nil
}

// listWaste devuelve el historial de mermas = UNION de warehouse_movements(waste,
// branch NULL) y supply_movements(waste, branch set), ordenado por created_at DESC.
func (s *store) listWaste(ctx context.Context, tenantID string, from, to *time.Time) ([]WasteItem, error) {
	args := []any{tenantID}
	filter := ""
	if from != nil {
		args = append(args, *from)
		filter += ` AND created_at >= $` + strconv.Itoa(len(args))
	}
	if to != nil {
		args = append(args, *to)
		filter += ` AND created_at < $` + strconv.Itoa(len(args))
	}
	q := `SELECT id, supply_id, supply_name, branch_id, branch_name, reason, quantity_base, origin, created_by_name, created_at
	        FROM (
	          SELECT m.id::text AS id, m.supply_id::text AS supply_id, sp.name AS supply_name,
	                 m.branch_id::text AS branch_id, b.name AS branch_name, m.reason AS reason,
	                 m.quantity_base AS quantity_base, 'warehouse' AS origin, u.name AS created_by_name,
	                 m.created_at AS created_at
	            FROM warehouse_movements m
	            JOIN supplies sp ON sp.id = m.supply_id
	            LEFT JOIN branches b ON b.id = m.branch_id
	            LEFT JOIN users u ON u.id = m.created_by
	           WHERE m.tenant_id = $1 AND m.type = 'waste'` + filter + `
	          UNION ALL
	          SELECT sm.id::text, sm.supply_id::text, sp.name, sm.branch_id::text, b.name, sm.reason,
	                 sm.quantity_base, 'branch', u.name, sm.created_at
	            FROM supply_movements sm
	            JOIN supplies sp ON sp.id = sm.supply_id
	            LEFT JOIN branches b ON b.id = sm.branch_id
	            LEFT JOIN users u ON u.id = sm.created_by
	           WHERE sm.tenant_id = $1 AND sm.type = 'waste'` + filter + `
	        ) x
	       ORDER BY created_at DESC LIMIT 100`

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []WasteItem{}
	for rows.Next() {
		var it WasteItem
		if err := rows.Scan(&it.ID, &it.SupplyID, &it.SupplyName, &it.BranchID, &it.BranchName,
			&it.Reason, &it.QuantityBase, &it.Origin, &it.CreatedByName, &it.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}
