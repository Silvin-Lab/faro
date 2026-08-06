package warehouse

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type fixture struct {
	svc  *Service
	pool *pgxpool.Pool
	// tenant A
	tenantA  string
	branchA1 string
	branchA2 string
	userA    string
	supplyA  string // package_content 900
	// tenant B (aislamiento)
	tenantB  string
	branchB1 string
	supplyB  string
}

func setup(t *testing.T) *fixture {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL no definido; se omiten tests de integración")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Skipf("pool de test: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Skipf("DB de test no disponible: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`TRUNCATE warehouse_movements, warehouse_stock, suppliers, supply_movements,
		 supply_branch_stock, product_supplies, supply_measures, supplies, supply_categories,
		 sale_items, sales, user_branches, products, categories, customers, users, branches,
		 tenants RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("truncate: %v", err)
	}

	f := &fixture{svc: NewService(pool), pool: pool}
	q := func(dst *string, sql string, args ...any) {
		if err := pool.QueryRow(ctx, sql, args...).Scan(dst); err != nil {
			t.Fatalf("seed (%s): %v", sql, err)
		}
	}
	q(&f.tenantA, "INSERT INTO tenants (name) VALUES ('A') RETURNING id::text")
	q(&f.tenantB, "INSERT INTO tenants (name) VALUES ('B') RETURNING id::text")
	q(&f.branchA1, "INSERT INTO branches (tenant_id, name) VALUES ($1,'A-Centro') RETURNING id::text", f.tenantA)
	q(&f.branchA2, "INSERT INTO branches (tenant_id, name) VALUES ($1,'A-Norte') RETURNING id::text", f.tenantA)
	q(&f.branchB1, "INSERT INTO branches (tenant_id, name) VALUES ($1,'B-Centro') RETURNING id::text", f.tenantB)
	q(&f.userA, "INSERT INTO users (tenant_id, email, password_hash, name, role) VALUES ($1,'a@t.test','x','Ana','branch_admin') RETURNING id::text", f.tenantA)
	q(&f.supplyA, "INSERT INTO supplies (tenant_id, name, base_unit, package_name, package_content) VALUES ($1,'Galletas','pieza','Caja 10',10) RETURNING id::text", f.tenantA)
	q(&f.supplyB, "INSERT INTO supplies (tenant_id, name, base_unit, package_name, package_content) VALUES ($1,'HarinaB','g','Bolsa 1kg',1000) RETURNING id::text", f.tenantB)
	return f
}

func (f *fixture) warehouseStock(t *testing.T, supplyID string) int {
	t.Helper()
	var stock int
	err := f.pool.QueryRow(context.Background(),
		`SELECT stock_base FROM warehouse_stock WHERE supply_id=$1`, supplyID).Scan(&stock)
	if errors.Is(err, pgx.ErrNoRows) {
		return -9999
	}
	if err != nil {
		t.Fatalf("warehouseStock: %v", err)
	}
	return stock
}

func (f *fixture) branchStock(t *testing.T, supplyID, branchID string) int {
	t.Helper()
	var stock int
	err := f.pool.QueryRow(context.Background(),
		`SELECT stock_base FROM supply_branch_stock WHERE supply_id=$1 AND branch_id=$2`,
		supplyID, branchID).Scan(&stock)
	if errors.Is(err, pgx.ErrNoRows) {
		return -9999
	}
	if err != nil {
		t.Fatalf("branchStock: %v", err)
	}
	return stock
}

// sumWarehouseMovements devuelve SUM(quantity_base) del ledger del almacén para un supply.
func (f *fixture) sumWarehouseMovements(t *testing.T, supplyID string) int {
	t.Helper()
	var sum int
	if err := f.pool.QueryRow(context.Background(),
		`SELECT COALESCE(SUM(quantity_base),0) FROM warehouse_movements WHERE supply_id=$1`,
		supplyID).Scan(&sum); err != nil {
		t.Fatalf("sumWarehouseMovements: %v", err)
	}
	return sum
}

// ---- B3: proveedores -------------------------------------------------------

func TestSupplierCRUD(t *testing.T) {
	f := setup(t)
	defer f.pool.Close()
	ctx := context.Background()

	addr := "  Calle 1  "
	sp, err := f.svc.CreateSupplier(ctx, f.tenantA, SupplierInput{Name: "  Lala  ", Address: &addr})
	if err != nil {
		t.Fatalf("crear proveedor: %v", err)
	}
	if sp.Name != "Lala" || sp.Status != "active" || sp.Address == nil || *sp.Address != "Calle 1" {
		t.Fatalf("proveedor inesperado: %+v", sp)
	}

	// Nombre vacío -> validación.
	if _, err := f.svc.CreateSupplier(ctx, f.tenantA, SupplierInput{Name: "  "}); !errors.Is(err, ErrValidation) {
		t.Fatalf("nombre vacío: esperaba ErrValidation, obtuvo %v", err)
	}
	// Duplicado por tenant -> name_taken.
	if _, err := f.svc.CreateSupplier(ctx, f.tenantA, SupplierInput{Name: "Lala"}); !errors.Is(err, ErrNameTaken) {
		t.Fatalf("duplicado: esperaba ErrNameTaken, obtuvo %v", err)
	}
	// Mismo nombre en otro tenant -> OK (aislamiento).
	if _, err := f.svc.CreateSupplier(ctx, f.tenantB, SupplierInput{Name: "Lala"}); err != nil {
		t.Fatalf("mismo nombre otro tenant: %v", err)
	}

	// Baja soft.
	inactive := "inactive"
	upd, err := f.svc.UpdateSupplier(ctx, f.tenantA, sp.ID, SupplierUpdate{Status: &inactive})
	if err != nil || upd.Status != "inactive" {
		t.Fatalf("baja soft: %+v err=%v", upd, err)
	}

	// Listado filtrado por status=active NO incluye el dado de baja.
	active := "active"
	list, err := f.svc.ListSuppliers(ctx, f.tenantA, &active)
	if err != nil {
		t.Fatalf("listar activos: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("activos: esperaba 0 tras baja, obtuvo %d", len(list))
	}
	// Sin filtro: aparece.
	all, _ := f.svc.ListSuppliers(ctx, f.tenantA, nil)
	if len(all) != 1 {
		t.Fatalf("todos: esperaba 1, obtuvo %d", len(all))
	}

	// PATCH de proveedor ajeno -> not found.
	if _, err := f.svc.UpdateSupplier(ctx, f.tenantB, sp.ID, SupplierUpdate{Name: strptr("X")}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("proveedor ajeno: esperaba ErrNotFound, obtuvo %v", err)
	}
}

// ---- B4: stock + mín/máx + to-buy -----------------------------------------

func TestMinMaxAndStatusAndToBuy(t *testing.T) {
	f := setup(t)
	defer f.pool.Close()
	ctx := context.Background()

	// Sin fila: status no_min, stock 0.
	list, err := f.svc.ListStock(ctx, f.tenantA, nil)
	if err != nil {
		t.Fatalf("listStock: %v", err)
	}
	if len(list) != 1 || list[0].StockBase != 0 || list[0].Status != "no_min" {
		t.Fatalf("stock inicial inesperado: %+v", list)
	}

	// max < min -> 400.
	if _, err := f.svc.UpdateMinMax(ctx, f.tenantA, f.supplyA, MinMaxUpdate{SetMin: true, Min: intptr(100), SetMax: true, Max: intptr(50)}); !errors.Is(err, ErrValidation) {
		t.Fatalf("max<min: esperaba ErrValidation, obtuvo %v", err)
	}

	// Setear solo min (fila lazy). stock 0 <= 100 -> below_min.
	it, err := f.svc.UpdateMinMax(ctx, f.tenantA, f.supplyA, MinMaxUpdate{SetMin: true, Min: intptr(100)})
	if err != nil {
		t.Fatalf("set min: %v", err)
	}
	if it.MinQuantity == nil || *it.MinQuantity != 100 || it.MaxQuantity != nil || it.Status != "below_min" {
		t.Fatalf("set min inesperado: %+v", it)
	}

	// Setear max solo, combinándose con el min persistido (100). max 50 < 100 -> 400.
	if _, err := f.svc.UpdateMinMax(ctx, f.tenantA, f.supplyA, MinMaxUpdate{SetMax: true, Max: intptr(50)}); !errors.Is(err, ErrValidation) {
		t.Fatalf("max<min persistido: esperaba ErrValidation, obtuvo %v", err)
	}
	// Setear max 200 OK.
	if _, err := f.svc.UpdateMinMax(ctx, f.tenantA, f.supplyA, MinMaxUpdate{SetMax: true, Max: intptr(200)}); err != nil {
		t.Fatalf("set max 200: %v", err)
	}

	// to-buy: min 100, stock 0 -> aparece con missing 100.
	tb, err := f.svc.ToBuy(ctx, f.tenantA)
	if err != nil {
		t.Fatalf("to-buy: %v", err)
	}
	if len(tb) != 1 || tb[0].Missing != 100 {
		t.Fatalf("to-buy inesperado: %+v", tb)
	}

	// Limpiar min (null) -> ya no aparece en to-buy, status no_min.
	it, err = f.svc.UpdateMinMax(ctx, f.tenantA, f.supplyA, MinMaxUpdate{SetMin: true, Min: nil})
	if err != nil {
		t.Fatalf("clear min: %v", err)
	}
	if it.MinQuantity != nil || it.Status != "no_min" {
		t.Fatalf("clear min inesperado: %+v", it)
	}
	tb, _ = f.svc.ToBuy(ctx, f.tenantA)
	if len(tb) != 0 {
		t.Fatalf("to-buy tras clear min: esperaba 0, obtuvo %d", len(tb))
	}

	// Supply ajeno -> not found.
	if _, err := f.svc.UpdateMinMax(ctx, f.tenantB, f.supplyA, MinMaxUpdate{SetMin: true, Min: intptr(1)}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("supply ajeno: esperaba ErrNotFound, obtuvo %v", err)
	}
}

// ---- B5: compras -----------------------------------------------------------

func TestPurchase(t *testing.T) {
	f := setup(t)
	defer f.pool.Close()
	ctx := context.Background()

	var supplierA string
	f.pool.QueryRow(ctx, "INSERT INTO suppliers (tenant_id, name) VALUES ($1,'Prov') RETURNING id::text", f.tenantA).Scan(&supplierA)

	// package_content de supplyA = 10. 3 cajas -> quantity_base 30.
	m, stock, err := f.svc.CreatePurchase(ctx, f.tenantA, PurchaseInput{
		SupplyID: f.supplyA, SupplierID: supplierA, Packages: 3, UnitCostCents: 500,
	}, f.userA)
	if err != nil {
		t.Fatalf("compra: %v", err)
	}
	if m.QuantityBase != 30 || stock != 30 {
		t.Fatalf("compra 3x10: esperaba qty=30 stock=30, obtuvo qty=%d stock=%d", m.QuantityBase, stock)
	}
	if m.Type != "purchase" || m.Packages == nil || *m.Packages != 3 || m.UnitCostCents == nil || *m.UnitCostCents != 500 {
		t.Fatalf("movimiento de compra inesperado: %+v", m)
	}

	// Segunda compra suma (upsert).
	_, stock2, _ := f.svc.CreatePurchase(ctx, f.tenantA, PurchaseInput{SupplyID: f.supplyA, SupplierID: supplierA, Packages: 1, UnitCostCents: 500}, f.userA)
	if stock2 != 40 {
		t.Fatalf("segunda compra: esperaba stock 40, obtuvo %d", stock2)
	}

	// Supply ajeno -> invalid_supply.
	if _, _, err := f.svc.CreatePurchase(ctx, f.tenantA, PurchaseInput{SupplyID: f.supplyB, SupplierID: supplierA, Packages: 1, UnitCostCents: 1}, f.userA); !errors.Is(err, ErrInvalidSupply) {
		t.Fatalf("supply ajeno: esperaba ErrInvalidSupply, obtuvo %v", err)
	}
	// Supplier ajeno -> invalid_supplier.
	var supplierB string
	f.pool.QueryRow(ctx, "INSERT INTO suppliers (tenant_id, name) VALUES ($1,'ProvB') RETURNING id::text", f.tenantB).Scan(&supplierB)
	if _, _, err := f.svc.CreatePurchase(ctx, f.tenantA, PurchaseInput{SupplyID: f.supplyA, SupplierID: supplierB, Packages: 1, UnitCostCents: 1}, f.userA); !errors.Is(err, ErrInvalidSupplier) {
		t.Fatalf("supplier ajeno: esperaba ErrInvalidSupplier, obtuvo %v", err)
	}

	// Historial: totalCents derivado = packages*unitCostCents.
	hist, err := f.svc.ListPurchases(ctx, f.tenantA, nil, nil)
	if err != nil {
		t.Fatalf("historial compras: %v", err)
	}
	if len(hist) != 2 {
		t.Fatalf("historial: esperaba 2, obtuvo %d", len(hist))
	}
	for _, h := range hist {
		if h.TotalCents == nil || *h.TotalCents != *h.Packages**h.UnitCostCents {
			t.Fatalf("totalCents mal derivado: %+v", h)
		}
	}
}

// TestPurchaseBackdating verifica el mapeo de fecha (§4.4): fecha pasada -> mediodía UTC.
func TestPurchaseBackdating(t *testing.T) {
	f := setup(t)
	defer f.pool.Close()
	ctx := context.Background()
	var supplierA string
	f.pool.QueryRow(ctx, "INSERT INTO suppliers (tenant_id, name) VALUES ($1,'Prov') RETURNING id::text", f.tenantA).Scan(&supplierA)

	m, _, err := f.svc.CreatePurchase(ctx, f.tenantA, PurchaseInput{
		SupplyID: f.supplyA, SupplierID: supplierA, Packages: 1, UnitCostCents: 100, Date: "2020-01-15",
	}, f.userA)
	if err != nil {
		t.Fatalf("compra backdate: %v", err)
	}
	got := m.CreatedAt.UTC()
	if got.Year() != 2020 || got.Month() != 1 || got.Day() != 15 || got.Hour() != 12 {
		t.Fatalf("backdating: esperaba 2020-01-15 12:00 UTC, obtuvo %s", got)
	}
}

// ---- B6: salidas -----------------------------------------------------------

func TestDispatch(t *testing.T) {
	f := setup(t)
	defer f.pool.Close()
	ctx := context.Background()

	// Salida de 100 a branchA1 sin stock previo: almacén -100, sucursal +100 (R4).
	m, whStock, branchStock, err := f.svc.CreateDispatch(ctx, f.tenantA, DispatchInput{
		SupplyID: f.supplyA, BranchID: f.branchA1, QuantityBase: 100,
	}, f.userA)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if m.QuantityBase != -100 || whStock != -100 || branchStock != 100 {
		t.Fatalf("dispatch: esperaba mov=-100 wh=-100 branch=100, obtuvo mov=%d wh=%d branch=%d", m.QuantityBase, whStock, branchStock)
	}

	// Se creó el transfer en supply_movements ligado al dispatch, con el mismo created_at.
	var transferQty int
	var whMovID string
	var transferAt time.Time
	err = f.pool.QueryRow(ctx,
		`SELECT quantity_base, warehouse_movement_id::text, created_at FROM supply_movements
		  WHERE supply_id=$1 AND branch_id=$2 AND type='transfer'`, f.supplyA, f.branchA1).
		Scan(&transferQty, &whMovID, &transferAt)
	if err != nil {
		t.Fatalf("transfer no encontrado: %v", err)
	}
	if transferQty != 100 || whMovID != m.ID {
		t.Fatalf("transfer inesperado: qty=%d whMovID=%s (dispatch %s)", transferQty, whMovID, m.ID)
	}
	if !transferAt.Equal(m.CreatedAt) {
		t.Fatalf("transfer no heredó created_at del dispatch: %s vs %s", transferAt, m.CreatedAt)
	}

	// Invariante del almacén: cache == suma del ledger.
	if f.warehouseStock(t, f.supplyA) != f.sumWarehouseMovements(t, f.supplyA) {
		t.Fatalf("invariante almacén rota: cache=%d ledger=%d", f.warehouseStock(t, f.supplyA), f.sumWarehouseMovements(t, f.supplyA))
	}

	// Branch ajeno -> invalid_branch.
	if _, _, _, err := f.svc.CreateDispatch(ctx, f.tenantA, DispatchInput{SupplyID: f.supplyA, BranchID: f.branchB1, QuantityBase: 1}, f.userA); !errors.Is(err, ErrInvalidBranch) {
		t.Fatalf("branch ajeno: esperaba ErrInvalidBranch, obtuvo %v", err)
	}
	// Sin branch -> invalid_branch.
	if _, _, _, err := f.svc.CreateDispatch(ctx, f.tenantA, DispatchInput{SupplyID: f.supplyA, BranchID: "", QuantityBase: 1}, f.userA); !errors.Is(err, ErrInvalidBranch) {
		t.Fatalf("sin branch: esperaba ErrInvalidBranch, obtuvo %v", err)
	}
}

// ---- B7 + regresión 100/10 galletas ----------------------------------------

// TestWasteBifurcationAndRegression cubre la bifurcación de merma (§3.3) y el
// escenario 100/10 galletas: dispatch 100 a S (almacén -100, S +100), luego waste
// branch=S de 10 -> almacén sigue en -100 (SIN déficit fantasma), S queda en 90.
func TestWasteBifurcationAndRegression(t *testing.T) {
	f := setup(t)
	defer f.pool.Close()
	ctx := context.Background()

	// Dispatch 100 a branchA1.
	if _, wh, br, err := f.svc.CreateDispatch(ctx, f.tenantA, DispatchInput{SupplyID: f.supplyA, BranchID: f.branchA1, QuantityBase: 100}, f.userA); err != nil || wh != -100 || br != 100 {
		t.Fatalf("dispatch 100: wh=%d br=%d err=%v", wh, br, err)
	}

	// Merma CON sucursal (branchA1) de 10: descuenta la sucursal, NO el almacén.
	m, stockBase, err := f.svc.CreateWaste(ctx, f.tenantA, WasteInput{
		SupplyID: f.supplyA, QuantityBase: 10, Reason: "se cayeron", BranchID: &f.branchA1,
	}, f.userA)
	if err != nil {
		t.Fatalf("waste branch: %v", err)
	}
	if m.Origin != "branch" || m.Type != "waste" || m.QuantityBase != -10 {
		t.Fatalf("waste branch mov inesperado: %+v", m)
	}
	if stockBase != 90 {
		t.Fatalf("waste branch: esperaba stock sucursal 90, obtuvo %d", stockBase)
	}

	// CLAVE de la regresión: el almacén sigue en -100 (solo por el dispatch), SIN
	// el déficit fantasma de -10; la sucursal quedó en 90.
	if got := f.warehouseStock(t, f.supplyA); got != -100 {
		t.Fatalf("REGRESIÓN: almacén debía seguir en -100 (sin déficit fantasma), obtuvo %d", got)
	}
	if got := f.branchStock(t, f.supplyA, f.branchA1); got != 90 {
		t.Fatalf("REGRESIÓN: sucursal debía quedar en 90, obtuvo %d", got)
	}
	// El almacén no recibió ningún movimiento de merma.
	var whWasteCount int
	f.pool.QueryRow(ctx, `SELECT count(*) FROM warehouse_movements WHERE supply_id=$1 AND type='waste'`, f.supplyA).Scan(&whWasteCount)
	if whWasteCount != 0 {
		t.Fatalf("REGRESIÓN: no debía haber waste en warehouse_movements, obtuvo %d", whWasteCount)
	}

	// Merma SIN sucursal de 5: descuenta el almacén (-100 -> -105), NO ninguna sucursal.
	m2, whStock, err := f.svc.CreateWaste(ctx, f.tenantA, WasteInput{
		SupplyID: f.supplyA, QuantityBase: 5, Reason: "caducó en almacén",
	}, f.userA)
	if err != nil {
		t.Fatalf("waste almacén: %v", err)
	}
	if m2.Origin != "warehouse" || whStock != -105 {
		t.Fatalf("waste almacén: esperaba origin=warehouse stock=-105, obtuvo origin=%s stock=%d", m2.Origin, whStock)
	}
	if got := f.branchStock(t, f.supplyA, f.branchA1); got != 90 {
		t.Fatalf("waste almacén no debía tocar la sucursal (sigue 90), obtuvo %d", got)
	}

	// reason obligatorio.
	if _, _, err := f.svc.CreateWaste(ctx, f.tenantA, WasteInput{SupplyID: f.supplyA, QuantityBase: 1, Reason: "  "}, f.userA); !errors.Is(err, ErrValidation) {
		t.Fatalf("reason vacío: esperaba ErrValidation, obtuvo %v", err)
	}
	// Branch ajeno en waste -> invalid_branch.
	if _, _, err := f.svc.CreateWaste(ctx, f.tenantA, WasteInput{SupplyID: f.supplyA, QuantityBase: 1, Reason: "x", BranchID: &f.branchB1}, f.userA); !errors.Is(err, ErrInvalidBranch) {
		t.Fatalf("branch ajeno en waste: esperaba ErrInvalidBranch, obtuvo %v", err)
	}

	// Historial de mermas = UNION (1 branch + 1 warehouse), ordenado por fecha DESC.
	waste, err := f.svc.ListWaste(ctx, f.tenantA, nil, nil)
	if err != nil {
		t.Fatalf("listWaste: %v", err)
	}
	if len(waste) != 2 {
		t.Fatalf("historial mermas: esperaba 2 (UNION), obtuvo %d", len(waste))
	}
	origins := map[string]bool{}
	for _, w := range waste {
		origins[w.Origin] = true
	}
	if !origins["branch"] || !origins["warehouse"] {
		t.Fatalf("historial mermas: esperaba ambos orígenes, obtuvo %+v", waste)
	}
}

// TestDispatchConcurrency verifica que dos salidas concurrentes del mismo insumo/
// sucursal no producen deadlock y dejan invariantes consistentes.
func TestDispatchConcurrency(t *testing.T) {
	f := setup(t)
	defer f.pool.Close()
	ctx := context.Background()

	const n = 20
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, _, err := f.svc.CreateDispatch(ctx, f.tenantA, DispatchInput{
				SupplyID: f.supplyA, BranchID: f.branchA1, QuantityBase: 5,
			}, f.userA)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("dispatch concurrente falló (posible deadlock): %v", err)
		}
	}
	// 20 salidas de 5: almacén -100, sucursal +100; caches == ledgers.
	if got := f.warehouseStock(t, f.supplyA); got != -n*5 {
		t.Fatalf("almacén tras concurrencia: esperaba %d, obtuvo %d", -n*5, got)
	}
	if got := f.branchStock(t, f.supplyA, f.branchA1); got != n*5 {
		t.Fatalf("sucursal tras concurrencia: esperaba %d, obtuvo %d", n*5, got)
	}
	if f.warehouseStock(t, f.supplyA) != f.sumWarehouseMovements(t, f.supplyA) {
		t.Fatalf("invariante almacén rota tras concurrencia")
	}
}

// countWarehouseMovements cuenta las filas del ledger del almacén para un supply.
func (f *fixture) countWarehouseMovements(t *testing.T, supplyID string) int {
	t.Helper()
	var n int
	if err := f.pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM warehouse_movements WHERE supply_id=$1`, supplyID).Scan(&n); err != nil {
		t.Fatalf("countWarehouseMovements: %v", err)
	}
	return n
}

// TestAdjustStock cubre el ajuste manual de existencia del almacén: incremento desde
// stock 0 (fila lazy), decremento, no-op (mismo valor => sin movimiento), validación
// de cantidad negativa, insumo ajeno/inexistente y el invariante
// stock_base == SUM(quantity_base) tras mezclarse con una compra.
func TestAdjustStock(t *testing.T) {
	f := setup(t)
	defer f.pool.Close()
	ctx := context.Background()

	// Incremento desde 0 (fila lazy inexistente): fija a 500.
	m, stock, err := f.svc.AdjustStock(ctx, f.tenantA, f.supplyA, AdjustInput{NewQuantity: 500}, f.userA)
	if err != nil {
		t.Fatalf("ajuste incremento: %v", err)
	}
	if m == nil || m.Type != "adjustment" || m.QuantityBase != 500 {
		t.Fatalf("movimiento incremento inesperado: %+v", m)
	}
	if stock != 500 {
		t.Fatalf("stock incremento: esperaba 500, obtuvo %d", stock)
	}
	if f.warehouseStock(t, f.supplyA) != f.sumWarehouseMovements(t, f.supplyA) {
		t.Fatalf("invariante rota tras incremento")
	}

	// Decremento: de 500 a 200 => movimiento -300.
	m, stock, err = f.svc.AdjustStock(ctx, f.tenantA, f.supplyA, AdjustInput{NewQuantity: 200}, f.userA)
	if err != nil {
		t.Fatalf("ajuste decremento: %v", err)
	}
	if m == nil || m.QuantityBase != -300 {
		t.Fatalf("movimiento decremento inesperado: %+v", m)
	}
	if stock != 200 || f.warehouseStock(t, f.supplyA) != 200 {
		t.Fatalf("stock decremento: esperaba 200, obtuvo %d", stock)
	}
	if f.warehouseStock(t, f.supplyA) != f.sumWarehouseMovements(t, f.supplyA) {
		t.Fatalf("invariante rota tras decremento")
	}

	// No-op: fijar al mismo valor (200) NO inserta movimiento.
	before := f.countWarehouseMovements(t, f.supplyA)
	m, stock, err = f.svc.AdjustStock(ctx, f.tenantA, f.supplyA, AdjustInput{NewQuantity: 200}, f.userA)
	if err != nil {
		t.Fatalf("ajuste no-op: %v", err)
	}
	if m != nil {
		t.Fatalf("no-op: esperaba movimiento nil, obtuvo %+v", m)
	}
	if stock != 200 {
		t.Fatalf("no-op: stock esperaba 200, obtuvo %d", stock)
	}
	if after := f.countWarehouseMovements(t, f.supplyA); after != before {
		t.Fatalf("no-op: no debía insertar movimiento (antes %d, después %d)", before, after)
	}

	// Cantidad negativa => ErrValidation (no toca nada).
	if _, _, err := f.svc.AdjustStock(ctx, f.tenantA, f.supplyA, AdjustInput{NewQuantity: -1}, f.userA); !errors.Is(err, ErrValidation) {
		t.Fatalf("negativo: esperaba ErrValidation, obtuvo %v", err)
	}

	// Insumo de otro tenant => ErrNotFound (aislamiento).
	if _, _, err := f.svc.AdjustStock(ctx, f.tenantA, f.supplyB, AdjustInput{NewQuantity: 10}, f.userA); !errors.Is(err, ErrNotFound) {
		t.Fatalf("insumo ajeno: esperaba ErrNotFound, obtuvo %v", err)
	}
	// uuid inexistente => ErrNotFound.
	if _, _, err := f.svc.AdjustStock(ctx, f.tenantA, "no-uuid", AdjustInput{NewQuantity: 10}, f.userA); !errors.Is(err, ErrNotFound) {
		t.Fatalf("insumo inexistente: esperaba ErrNotFound, obtuvo %v", err)
	}

	// Invariante tras mezclar con una compra: compra de 3 cajas (package_content 10)
	// => +30 (stock 230), luego ajuste a 1000. stock_base debe seguir == SUM(ledger).
	sp, err := f.svc.CreateSupplier(ctx, f.tenantA, SupplierInput{Name: "Prov"})
	if err != nil {
		t.Fatalf("crear proveedor: %v", err)
	}
	if _, _, err := f.svc.CreatePurchase(ctx, f.tenantA, PurchaseInput{
		SupplyID: f.supplyA, SupplierID: sp.ID, Packages: 3, UnitCostCents: 100,
	}, f.userA); err != nil {
		t.Fatalf("compra: %v", err)
	}
	if _, stock, err = f.svc.AdjustStock(ctx, f.tenantA, f.supplyA, AdjustInput{NewQuantity: 1000}, f.userA); err != nil {
		t.Fatalf("ajuste tras compra: %v", err)
	}
	if stock != 1000 {
		t.Fatalf("stock tras compra+ajuste: esperaba 1000, obtuvo %d", stock)
	}
	if f.warehouseStock(t, f.supplyA) != f.sumWarehouseMovements(t, f.supplyA) {
		t.Fatalf("invariante rota tras compra+ajuste")
	}
}

func strptr(s string) *string { return &s }
func intptr(i int) *int       { return &i }
