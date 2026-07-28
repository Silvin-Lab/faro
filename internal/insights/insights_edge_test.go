package insights

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// insights_edge_test.go — matriz completa de casos borde (tasks Q2/Q3/Q4 +
// regresión de esquema). Los tests operan a nivel de service/store contra una DB
// de prueba real: cada test siembra su propio escenario y arma el Scope a mano,
// para controlar exactamente el caso borde (costo NULL, sin receta, producto
// borrado, retorno más allá de `to`, aislamiento por tenant, determinismo, etc.).
// El camino feliz (shape) vive en insights_test.go; el gating (401/403/400) en
// internal/server/insights_gating_test.go (necesita el middleware de auth real).

// world es un ayudante de siembra sobre la DB de prueba. Cada test arranca con un
// newWorld que trunca todo y devuelve helpers para insertar filas mínimas.
type world struct {
	t    *testing.T
	ctx  context.Context
	pool *pgxpool.Pool
	svc  *Service
}

func newWorld(t *testing.T) *world {
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
		"TRUNCATE warehouse_movements, warehouse_stock, suppliers, supply_movements, supply_branch_stock, product_supplies, supply_measures, supplies, supply_categories, user_branches, loyalty_redemptions, loyalty_promotion_products, loyalty_promotions, sale_items, sales, customers, products, categories, branches, users, tenants RESTART IDENTITY CASCADE"); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	return &world{t: t, ctx: ctx, pool: pool, svc: NewService(pool)}
}

func (w *world) close() { w.pool.Close() }

func (w *world) scan(sql string, args ...any) string {
	var id string
	if err := w.pool.QueryRow(w.ctx, sql, args...).Scan(&id); err != nil {
		w.t.Fatalf("seed (%s): %v", sql, err)
	}
	return id
}

func (w *world) exec(sql string, args ...any) {
	if _, err := w.pool.Exec(w.ctx, sql, args...); err != nil {
		w.t.Fatalf("seed exec (%s): %v", sql, err)
	}
}

func (w *world) tenant(name string) string {
	return w.scan("INSERT INTO tenants (name) VALUES ($1) RETURNING id::text", name)
}

func (w *world) branch(tenant, name string) string {
	return w.scan("INSERT INTO branches (tenant_id, name) VALUES ($1,$2) RETURNING id::text", tenant, name)
}

func (w *world) product(tenant, name string, price int) string {
	return w.scan("INSERT INTO products (tenant_id, name, price_cents) VALUES ($1,$2,$3) RETURNING id::text", tenant, name, price)
}

// supply crea un insumo. cost=nil => package_cost_cents NULL (costo no capturado).
func (w *world) supply(tenant, name string, content int, cost *int) string {
	return w.scan(
		"INSERT INTO supplies (tenant_id, name, base_unit, package_name, package_content, package_cost_cents) VALUES ($1,$2,'ml','Paquete',$3,$4) RETURNING id::text",
		tenant, name, content, cost)
}

func (w *world) recipe(tenant, product, supply string, qtyBase int) {
	w.exec("INSERT INTO product_supplies (tenant_id, product_id, supply_id, quantity_base) VALUES ($1,$2,$3,$4)", tenant, product, supply, qtyBase)
}

func (w *world) customer(tenant, phone string) string {
	return w.scan("INSERT INTO customers (tenant_id, phone, first_name, last_name) VALUES ($1,$2,'N','N') RETURNING id::text", tenant, phone)
}

// sale inserta una venta. customer/branch nil => columna NULL (anónima / sin sucursal).
func (w *world) sale(tenant string, customer, branch *string, total int, at time.Time) string {
	return w.scan(
		"INSERT INTO sales (tenant_id, customer_id, branch_id, total_cents, amount_paid_cents, change_cents, payment_method, created_at) VALUES ($1,$2,$3,$4,$4,0,'cash',$5) RETURNING id::text",
		tenant, customer, branch, total, at)
}

// item inserta una línea de venta. product=nil => product_id NULL (producto borrado).
func (w *world) item(sale string, product *string, name string, price, qty int) {
	w.exec("INSERT INTO sale_items (sale_id, product_id, name, unit_price_cents, quantity, line_total_cents) VALUES ($1,$2,$3,$4,$5,$6)",
		sale, product, name, price, qty, price*qty)
}

func (w *world) redeem(tenant, customer, sale string) {
	w.exec(`INSERT INTO loyalty_redemptions
		(tenant_id, customer_id, sale_id, promotion_name, discount_percent, visit_threshold, caused_reset, visits_cycle_at, visits_lifetime_at, discount_cents)
		VALUES ($1,$2,$3,'Promo',10,3,false,3,3,500)`, tenant, customer, sale)
}

func ptr[T any](v T) *T { return &v }

// ============================================================================
// Q2 — Insight 1: Recurrencia
// ============================================================================

// La tasa se calcula SOLO sobre identificados; las anónimas van al bucket aparte
// y nunca entran en la tasa.
func TestRecurrenceRateOnlyIdentified(t *testing.T) {
	w := newWorld(t)
	defer w.close()
	a := w.tenant("A")
	now := time.Now()
	from := now.AddDate(0, 0, -30)
	to := now.AddDate(0, 0, 1)

	// C1 recurrente (2 ventas), C2 nuevo (1 venta) => with_ge1=2, with_ge2=1 => 50%.
	c1 := w.customer(a, "111")
	c2 := w.customer(a, "222")
	w.sale(a, &c1, nil, 5000, now.AddDate(0, 0, -20))
	w.sale(a, &c1, nil, 5000, now.AddDate(0, 0, -10))
	w.sale(a, &c2, nil, 5000, now.AddDate(0, 0, -5))
	// 3 ventas anónimas: deben quedar FUERA de la tasa, dentro del bucket anónimas.
	w.sale(a, nil, nil, 2000, now.AddDate(0, 0, -4))
	w.sale(a, nil, nil, 2000, now.AddDate(0, 0, -3))
	w.sale(a, nil, nil, 2000, now.AddDate(0, 0, -2))

	res, err := w.svc.Recurrence(w.ctx, Scope{TenantID: a, From: from, To: to})
	if err != nil {
		t.Fatalf("recurrence: %v", err)
	}
	if res.WithGe1 != 2 || res.WithGe2 != 1 {
		t.Fatalf("identificados: esperaba 2/1, obtuvo %d/%d", res.WithGe1, res.WithGe2)
	}
	if res.RatePct != 50 {
		t.Fatalf("ratePct: esperaba 50 (solo identificados), obtuvo %v", res.RatePct)
	}
	if res.Empty {
		t.Fatal("empty: esperaba false")
	}
	// Bucket anónimas: 3 anónimas de 6 ventas totales => 50%.
	if res.AnonSales != 3 || res.TotalSales != 6 {
		t.Fatalf("anónimas: esperaba 3/6, obtuvo %d/%d", res.AnonSales, res.TotalSales)
	}
	if res.AnonSharePct != 50 {
		t.Fatalf("anonSharePct: esperaba 50, obtuvo %v", res.AnonSharePct)
	}
}

// Cohorte: cliente cuya 1ª venta cae en el rango y cuyo retorno es al día 45
// (POSTERIOR a `to`) => cuenta en ret60/ret90 pero NO en ret30, y el retorno más
// allá de `to` se cuenta (F3).
func TestRecurrenceCohortReturnBeyondTo(t *testing.T) {
	w := newWorld(t)
	defer w.close()
	a := w.tenant("A")
	base := time.Now().AddDate(0, 0, -60)
	from := base
	to := base.AddDate(0, 0, 20) // ventana corta: el retorno cae fuera

	c1 := w.customer(a, "111")
	// 1ª venta dentro del rango; 2ª venta al día 45 (base+50 > to=base+20).
	w.sale(a, &c1, nil, 5000, base.AddDate(0, 0, 5))
	w.sale(a, &c1, nil, 5000, base.AddDate(0, 0, 50))

	res, err := w.svc.Recurrence(w.ctx, Scope{TenantID: a, From: from, To: to})
	if err != nil {
		t.Fatalf("recurrence: %v", err)
	}
	if res.Cohort.N != 1 {
		t.Fatalf("cohorte N: esperaba 1, obtuvo %d", res.Cohort.N)
	}
	if res.Cohort.Ret30 != 0 {
		t.Fatalf("ret30: retorno al día 45 NO debe contar en 30, obtuvo %d", res.Cohort.Ret30)
	}
	if res.Cohort.Ret60 != 1 || res.Cohort.Ret90 != 1 {
		t.Fatalf("ret60/ret90: retorno al día 45 (más allá de `to`) debe contar, obtuvo %d/%d", res.Cohort.Ret60, res.Cohort.Ret90)
	}
}

// Rango sin identificados => empty:true, RatePct=0 sin división por cero, y el
// bucket anónimas sigue presente.
func TestRecurrenceEmptyNoIdentified(t *testing.T) {
	w := newWorld(t)
	defer w.close()
	a := w.tenant("A")
	now := time.Now()
	from := now.AddDate(0, 0, -30)
	to := now.AddDate(0, 0, 1)

	// Solo ventas anónimas en el rango.
	w.sale(a, nil, nil, 2000, now.AddDate(0, 0, -5))
	w.sale(a, nil, nil, 3000, now.AddDate(0, 0, -4))

	res, err := w.svc.Recurrence(w.ctx, Scope{TenantID: a, From: from, To: to})
	if err != nil {
		t.Fatalf("recurrence: %v", err)
	}
	if !res.Empty {
		t.Fatal("empty: esperaba true sin identificados")
	}
	if res.WithGe1 != 0 || res.RatePct != 0 {
		t.Fatalf("sin identificados: esperaba withGe1=0 ratePct=0, obtuvo %d/%v", res.WithGe1, res.RatePct)
	}
	if res.AnonSales != 2 || res.TotalSales != 2 {
		t.Fatalf("bucket anónimas debe persistir: esperaba 2/2, obtuvo %d/%d", res.AnonSales, res.TotalSales)
	}
}

// ============================================================================
// Q2 — Insight 2: Producto estrella (T4 costo de receta)
// ============================================================================

// Margen: el cálculo suma TODOS los insumos con costo (numeric) y redondea al
// final. Prueba la aritmética exacta (revenue - round(unitCost*units)).
func TestTopProductsMarginArithmetic(t *testing.T) {
	w := newWorld(t)
	defer w.close()
	a := w.tenant("A")
	now := time.Now()
	from := now.AddDate(0, 0, -30)
	to := now.AddDate(0, 0, 1)

	latte := w.product(a, "Latte", 5000)
	// Dos insumos con costo: Leche 200*2000/1000=400 ; Cafe 50*5000/500=500 => 900/u.
	leche := w.supply(a, "Leche", 1000, ptr(2000))
	cafe := w.supply(a, "Cafe", 500, ptr(5000))
	w.recipe(a, latte, leche, 200)
	w.recipe(a, latte, cafe, 50)

	// 3 unidades vendidas => revenue 15000; costo 900*3=2700; margen 12300.
	s := w.sale(a, nil, nil, 15000, now.AddDate(0, 0, -5))
	w.item(s, &latte, "Latte", 5000, 3)

	res, err := w.svc.TopProducts(w.ctx, Scope{TenantID: a, From: from, To: to})
	if err != nil {
		t.Fatalf("top-products: %v", err)
	}
	if len(res.ByMargin) != 1 {
		t.Fatalf("byMargin: esperaba 1 fila, obtuvo %+v", res.ByMargin)
	}
	m := res.ByMargin[0]
	if m.Name != "Latte" || m.RevenueCents != 15000 || m.MarginCents != 12300 {
		t.Fatalf("margen Latte: esperaba rev=15000 margin=12300, obtuvo %+v", m)
	}
	if len(res.ExcludedFromMargin) != 0 {
		t.Fatalf("no debería haber excluidos, obtuvo %+v", res.ExcludedFromMargin)
	}
}

// Un insumo con package_cost_cents NULL => el producto se EXCLUYE del margen con
// reason null_cost (bool_or), y NO se lo trata como costo 0 (no aparece en byMargin
// con margen=revenue). Sin receta => no_recipe pero presente en ingresos/volumen.
// Producto borrado (product_id NULL) => cuenta por snapshot, fuera de margen.
func TestTopProductsExclusionReasons(t *testing.T) {
	w := newWorld(t)
	defer w.close()
	a := w.tenant("A")
	now := time.Now()
	from := now.AddDate(0, 0, -30)
	to := now.AddDate(0, 0, 1)

	latte := w.product(a, "Latte", 5000) // receta completa => en margen
	agua := w.product(a, "Agua", 2000)   // receta con insumo sin costo => null_cost
	muffin := w.product(a, "Muffin", 3000)
	_ = muffin // sin receta => no_recipe

	leche := w.supply(a, "Leche", 1000, ptr(2000))
	cacao := w.supply(a, "Cacao", 500, nil) // costo NULL
	w.recipe(a, latte, leche, 200)
	w.recipe(a, agua, cacao, 10)

	s := w.sale(a, nil, nil, 10000, now.AddDate(0, 0, -5))
	w.item(s, &latte, "Latte", 5000, 1)
	w.item(s, &agua, "Agua", 2000, 1)
	w.item(s, &muffin, "Muffin", 3000, 1)
	// Producto borrado: línea con product_id NULL, nombre por snapshot.
	w.item(s, nil, "Combo viejo", 4000, 1)

	res, err := w.svc.TopProducts(w.ctx, Scope{TenantID: a, From: from, To: to})
	if err != nil {
		t.Fatalf("top-products: %v", err)
	}

	// Ingresos/volumen: los 4 (incl. borrado por snapshot) están presentes.
	names := map[string]bool{}
	for _, p := range res.ByRevenue {
		names[p.Name] = true
	}
	for _, n := range []string{"Latte", "Agua", "Muffin", "Combo viejo"} {
		if !names[n] {
			t.Fatalf("ingresos: esperaba incluir %q, obtuvo %+v", n, res.ByRevenue)
		}
	}

	// Margen: SOLO Latte (con costo capturado). Agua/Muffin/Combo fuera.
	if len(res.ByMargin) != 1 || res.ByMargin[0].Name != "Latte" {
		t.Fatalf("byMargin: esperaba solo Latte, obtuvo %+v", res.ByMargin)
	}

	reasons := map[string]string{}
	for _, e := range res.ExcludedFromMargin {
		reasons[e.Name] = e.Reason
	}
	if reasons["Agua"] != "null_cost" {
		t.Fatalf("Agua (insumo sin costo) => null_cost, obtuvo %q", reasons["Agua"])
	}
	if reasons["Muffin"] != "no_recipe" {
		t.Fatalf("Muffin (sin receta) => no_recipe, obtuvo %q", reasons["Muffin"])
	}
	if reasons["Combo viejo"] != "no_recipe" {
		t.Fatalf("Combo borrado => no_recipe, obtuvo %q", reasons["Combo viejo"])
	}
}

// Margen e ingresos respetan el filtro por sucursal.
func TestTopProductsBranchScope(t *testing.T) {
	w := newWorld(t)
	defer w.close()
	a := w.tenant("A")
	now := time.Now()
	from := now.AddDate(0, 0, -30)
	to := now.AddDate(0, 0, 1)

	b1 := w.branch(a, "Centro")
	b2 := w.branch(a, "Norte")
	latte := w.product(a, "Latte", 5000)
	pan := w.product(a, "Pan", 3000)

	s1 := w.sale(a, nil, &b1, 5000, now.AddDate(0, 0, -5))
	w.item(s1, &latte, "Latte", 5000, 1)
	s2 := w.sale(a, nil, &b2, 3000, now.AddDate(0, 0, -5))
	w.item(s2, &pan, "Pan", 3000, 1)

	res, err := w.svc.TopProducts(w.ctx, Scope{TenantID: a, From: from, To: to, Branch: BranchFilter{ID: &b1}})
	if err != nil {
		t.Fatalf("top-products b1: %v", err)
	}
	if len(res.ByRevenue) != 1 || res.ByRevenue[0].Name != "Latte" {
		t.Fatalf("filtro sucursal b1: esperaba solo Latte, obtuvo %+v", res.ByRevenue)
	}
}

// ============================================================================
// Q2 — Insight 3: Ticket por segmento
// ============================================================================

// Partición exhaustiva y disjunta: new(1 venta) + recurring(>=2) = ventas
// identificadas; anónimas aparte. Mismo umbral que Insight 1.
func TestTicketSegmentsPartition(t *testing.T) {
	w := newWorld(t)
	defer w.close()
	a := w.tenant("A")
	now := time.Now()
	from := now.AddDate(0, 0, -30)
	to := now.AddDate(0, 0, 1)

	// Nuevo: 1 venta de 4000.
	cn := w.customer(a, "100")
	w.sale(a, &cn, nil, 4000, now.AddDate(0, 0, -5))
	// Recurrente: 2 ventas (6000 + 4000 = 10000).
	cr := w.customer(a, "200")
	w.sale(a, &cr, nil, 6000, now.AddDate(0, 0, -8))
	w.sale(a, &cr, nil, 4000, now.AddDate(0, 0, -4))
	// Anónimas: 2 ventas (2000 + 4000).
	w.sale(a, nil, nil, 2000, now.AddDate(0, 0, -3))
	w.sale(a, nil, nil, 4000, now.AddDate(0, 0, -2))

	res, err := w.svc.TicketSegments(w.ctx, Scope{TenantID: a, From: from, To: to})
	if err != nil {
		t.Fatalf("ticket-segments: %v", err)
	}
	if res.New.Sales != 1 || res.New.AvgCents != 4000 {
		t.Fatalf("nuevo: esperaba 1 venta / 4000, obtuvo %+v", res.New)
	}
	if res.Recurring.Sales != 2 || res.Recurring.AvgCents != 5000 {
		t.Fatalf("recurrente: esperaba 2 ventas / 5000, obtuvo %+v", res.Recurring)
	}
	if res.Anonymous.Sales != 2 || res.Anonymous.AvgCents != 3000 {
		t.Fatalf("anónimas: esperaba 2 ventas / 3000, obtuvo %+v", res.Anonymous)
	}
	// Exhaustividad: identificadas = new + recurring (3), anónimas aparte (2).
	if res.New.Sales+res.Recurring.Sales != 3 {
		t.Fatalf("partición no exhaustiva: new+recurring=%d, esperaba 3", res.New.Sales+res.Recurring.Sales)
	}
}

// Segmento sin ventas => avg=0 ("—" en UI) y sales=0 sin romper (guarda de división).
func TestTicketSegmentsEmptySegment(t *testing.T) {
	w := newWorld(t)
	defer w.close()
	a := w.tenant("A")
	now := time.Now()
	from := now.AddDate(0, 0, -30)
	to := now.AddDate(0, 0, 1)

	// Solo un cliente nuevo, sin recurrentes ni anónimas.
	cn := w.customer(a, "100")
	w.sale(a, &cn, nil, 4000, now.AddDate(0, 0, -5))

	res, err := w.svc.TicketSegments(w.ctx, Scope{TenantID: a, From: from, To: to})
	if err != nil {
		t.Fatalf("ticket-segments: %v", err)
	}
	if res.Recurring.Sales != 0 || res.Recurring.AvgCents != 0 {
		t.Fatalf("recurrente vacío: esperaba 0/0, obtuvo %+v", res.Recurring)
	}
	if res.Anonymous.Sales != 0 || res.Anonymous.AvgCents != 0 {
		t.Fatalf("anónimas vacío: esperaba 0/0, obtuvo %+v", res.Anonymous)
	}
	if res.New.Sales != 1 {
		t.Fatalf("nuevo: esperaba 1 venta, obtuvo %+v", res.New)
	}
}

// ============================================================================
// Q3 — Insight 4: 2ª visita
// ============================================================================

// Numeración GLOBAL: la 2ª visita es la 2ª del historial aunque la 1ª sea previa
// al rango. Con >=5 segundas visitas en el rango la muestra alcanza; un producto
// con cnt2<3 no se lista; anónimas nunca aparecen; orden por over_rep desc.
func TestSecondVisitGlobalNumbering(t *testing.T) {
	w := newWorld(t)
	defer w.close()
	a := w.tenant("A")
	base := time.Now().AddDate(0, 0, -40)
	from := base
	to := base.AddDate(0, 0, 30)

	latte := w.product(a, "Latte", 5000)
	pan := w.product(a, "Pan", 3000)
	muffin := w.product(a, "Muffin", 3000)

	// 5 clientes: 1ª venta ANTES del rango, 2ª venta DENTRO del rango.
	for i := 0; i < 5; i++ {
		c := w.customer(a, "c"+string(rune('a'+i)))
		w.sale(a, &c, nil, 5000, base.AddDate(0, 0, -10)) // visita #1 (previa al rango)
		s2 := w.sale(a, &c, nil, 5000, base.AddDate(0, 0, 2))
		w.item(s2, &latte, "Latte", 5000, 1) // Latte en las 5 segundas visitas
		w.item(s2, &pan, "Pan", 3000, 1)      // Pan también en las 5
		if i < 2 {
			w.item(s2, &muffin, "Muffin", 3000, 1) // Muffin solo en 2 => cnt2<3
		}
	}
	// Venta anónima en el rango con Muffin: no debe aparecer (sin customer_id).
	sAnon := w.sale(a, nil, nil, 3000, base.AddDate(0, 0, 3))
	w.item(sAnon, &muffin, "Muffin", 3000, 1)

	res, err := w.svc.SecondVisit(w.ctx, Scope{TenantID: a, From: from, To: to})
	if err != nil {
		t.Fatalf("second-visit: %v", err)
	}
	if res.Insufficient {
		t.Fatalf("esperaba muestra suficiente (5 segundas visitas), obtuvo insufficient")
	}
	if res.SampleSize != 5 {
		t.Fatalf("sampleSize: esperaba 5, obtuvo %d", res.SampleSize)
	}
	got := map[string]int{}
	for _, it := range res.Items {
		got[it.Name] = it.Support
	}
	if got["Latte"] != 5 {
		t.Fatalf("Latte: esperaba support 5 en 2ª visita, obtuvo %d (items=%+v)", got["Latte"], res.Items)
	}
	if got["Pan"] != 5 {
		t.Fatalf("Pan: esperaba support 5, obtuvo %d", got["Pan"])
	}
	if _, ok := got["Muffin"]; ok {
		t.Fatalf("Muffin (cnt2=2 < 3) no debía listarse, items=%+v", res.Items)
	}
	// Orden por over_rep descendente (no creciente).
	for i := 1; i < len(res.Items); i++ {
		if res.Items[i-1].OverRep < res.Items[i].OverRep {
			t.Fatalf("orden over_rep no descendente: %+v", res.Items)
		}
	}
}

// n2_total < umbral (default 5) => insufficient:true sin lista.
func TestSecondVisitInsufficient(t *testing.T) {
	w := newWorld(t)
	defer w.close()
	a := w.tenant("A")
	base := time.Now().AddDate(0, 0, -40)
	from := base
	to := base.AddDate(0, 0, 30)

	latte := w.product(a, "Latte", 5000)
	// Solo 2 clientes con 2ª visita en el rango (< 5).
	for i := 0; i < 2; i++ {
		c := w.customer(a, "z"+string(rune('a'+i)))
		w.sale(a, &c, nil, 5000, base.AddDate(0, 0, -10))
		s2 := w.sale(a, &c, nil, 5000, base.AddDate(0, 0, 2))
		w.item(s2, &latte, "Latte", 5000, 1)
	}

	res, err := w.svc.SecondVisit(w.ctx, Scope{TenantID: a, From: from, To: to})
	if err != nil {
		t.Fatalf("second-visit: %v", err)
	}
	if !res.Insufficient {
		t.Fatalf("esperaba insufficient con 2 segundas visitas, obtuvo %+v", res)
	}
	if res.SampleSize != 2 {
		t.Fatalf("sampleSize: esperaba 2, obtuvo %d", res.SampleSize)
	}
	if len(res.Items) != 0 {
		t.Fatalf("items: esperaba lista vacía, obtuvo %+v", res.Items)
	}
}

// ============================================================================
// Q3 — Insight 5: Afinidad de canasta
// ============================================================================

// Par >= umbral aparece con support/lift correctos; par < umbral ausente; sin
// auto-par ni (A,B)/(B,A) duplicado.
func TestBasketAffinityThresholdAndLift(t *testing.T) {
	w := newWorld(t)
	defer w.close()
	a := w.tenant("A")
	now := time.Now()
	from := now.AddDate(0, 0, -30)
	to := now.AddDate(0, 0, 1)

	pa := w.product(a, "A", 1000)
	pb := w.product(a, "B", 1000)
	pc := w.product(a, "C", 1000)

	// 3 ventas {A,B} (>= umbral 3) y 2 ventas {A,C} (< umbral).
	mk := func(p1, p2 string, n int, dayOff int) {
		for i := 0; i < n; i++ {
			s := w.sale(a, nil, nil, 2000, now.AddDate(0, 0, dayOff-i))
			w.item(s, &p1, "x", 1000, 1)
			w.item(s, &p2, "y", 1000, 1)
		}
	}
	mk(pa, pb, 3, -5)
	mk(pa, pc, 2, -15)

	res, err := w.svc.BasketAffinity(w.ctx, Scope{TenantID: a, From: from, To: to})
	if err != nil {
		t.Fatalf("basket-affinity: %v", err)
	}
	if res.Insufficient {
		t.Fatalf("esperaba par suficiente {A,B}, obtuvo insufficient")
	}
	if len(res.Items) != 1 {
		t.Fatalf("esperaba exactamente 1 par (A,B); (A,C) bajo umbral no debe aparecer; obtuvo %+v", res.Items)
	}
	p := res.Items[0]
	set := map[string]bool{p.A: true, p.B: true}
	if !set["A"] || !set["B"] || p.A == p.B {
		t.Fatalf("par: esperaba {A,B} sin auto-par, obtuvo %+v", p)
	}
	if p.Support != 3 {
		t.Fatalf("support: esperaba 3, obtuvo %d", p.Support)
	}
	// freq(A)=5, freq(B)=3, N=5 distinct sales => lift = 3*5/(5*3) = 1.0.
	if p.Lift < 0.99 || p.Lift > 1.01 {
		t.Fatalf("lift: esperaba ~1.0, obtuvo %v", p.Lift)
	}
}

// Productos borrados (product_id NULL) no forman pares.
func TestBasketAffinityDeletedNoPair(t *testing.T) {
	w := newWorld(t)
	defer w.close()
	a := w.tenant("A")
	now := time.Now()
	from := now.AddDate(0, 0, -30)
	to := now.AddDate(0, 0, 1)

	pa := w.product(a, "A", 1000)
	pb := w.product(a, "B", 1000)

	// 3 ventas {A,B} => par válido.
	for i := 0; i < 3; i++ {
		s := w.sale(a, nil, nil, 2000, now.AddDate(0, 0, -5-i))
		w.item(s, &pa, "A", 1000, 1)
		w.item(s, &pb, "B", 1000, 1)
	}
	// 3 ventas {A, borrado} => el borrado NO debe emparejar con A.
	for i := 0; i < 3; i++ {
		s := w.sale(a, nil, nil, 2000, now.AddDate(0, 0, -10-i))
		w.item(s, &pa, "A", 1000, 1)
		w.item(s, nil, "Borrado", 1000, 1)
	}

	res, err := w.svc.BasketAffinity(w.ctx, Scope{TenantID: a, From: from, To: to})
	if err != nil {
		t.Fatalf("basket-affinity: %v", err)
	}
	if len(res.Items) != 1 {
		t.Fatalf("esperaba solo el par (A,B); el borrado no forma par; obtuvo %+v", res.Items)
	}
	for _, it := range res.Items {
		if it.A == "Borrado" || it.B == "Borrado" {
			t.Fatalf("un producto borrado formó par: %+v", it)
		}
	}
}

// Sin pares que alcancen el soporte => insufficient:true.
func TestBasketAffinityInsufficient(t *testing.T) {
	w := newWorld(t)
	defer w.close()
	a := w.tenant("A")
	now := time.Now()
	from := now.AddDate(0, 0, -30)
	to := now.AddDate(0, 0, 1)

	pa := w.product(a, "A", 1000)
	pb := w.product(a, "B", 1000)
	// Solo 1 venta con el par => support 1 < 3.
	s := w.sale(a, nil, nil, 2000, now.AddDate(0, 0, -5))
	w.item(s, &pa, "A", 1000, 1)
	w.item(s, &pb, "B", 1000, 1)

	res, err := w.svc.BasketAffinity(w.ctx, Scope{TenantID: a, From: from, To: to})
	if err != nil {
		t.Fatalf("basket-affinity: %v", err)
	}
	if !res.Insufficient {
		t.Fatalf("esperaba insufficient sin pares suficientes, obtuvo %+v", res.Items)
	}
	if len(res.Items) != 0 {
		t.Fatalf("items: esperaba vacío, obtuvo %+v", res.Items)
	}
}

// ============================================================================
// Q3 — Insight 6: Efectividad de lealtad + regresión de esquema
// ============================================================================

// El segmento "canjeó" se define por loyalty_redemptions: un cliente con >=1
// redención en el rango cae en "canjeó" para TODAS sus ventas del rango. Anónimas
// excluidas.
func TestLoyaltyEffectRedemptionDefinesSegment(t *testing.T) {
	w := newWorld(t)
	defer w.close()
	a := w.tenant("A")
	now := time.Now()
	from := now.AddDate(0, 0, -30)
	to := now.AddDate(0, 0, 1)

	// Cliente R: 3 ventas, 1 con redención => segmento canjeó (3 ventas cuentan).
	cr := w.customer(a, "111")
	w.sale(a, &cr, nil, 5000, now.AddDate(0, 0, -20))
	w.sale(a, &cr, nil, 6000, now.AddDate(0, 0, -12))
	s3 := w.sale(a, &cr, nil, 7000, now.AddDate(0, 0, -6))
	w.redeem(a, cr, s3)
	// Cliente N: 2 ventas, sin redención => no canjeó.
	cn := w.customer(a, "222")
	w.sale(a, &cn, nil, 4000, now.AddDate(0, 0, -10))
	w.sale(a, &cn, nil, 4000, now.AddDate(0, 0, -5))
	// Anónima: excluida.
	w.sale(a, nil, nil, 9000, now.AddDate(0, 0, -3))

	res, err := w.svc.LoyaltyEffect(w.ctx, Scope{TenantID: a, From: from, To: to})
	if err != nil {
		t.Fatalf("loyalty-effect: %v", err)
	}
	if res.Redeemed.Customers != 1 || res.Redeemed.SalesTotal != 3 || res.Redeemed.SpendTotal != 18000 {
		t.Fatalf("canjeó: esperaba 1 cliente / 3 ventas / 18000, obtuvo %+v", res.Redeemed)
	}
	if res.NotRedeemed.Customers != 1 || res.NotRedeemed.SalesTotal != 2 || res.NotRedeemed.SpendTotal != 8000 {
		t.Fatalf("no canjeó: esperaba 1 cliente / 2 ventas / 8000, obtuvo %+v", res.NotRedeemed)
	}
	// Anónima (9000) no debe entrar en ningún segmento.
	if res.Redeemed.SpendTotal+res.NotRedeemed.SpendTotal != 26000 {
		t.Fatalf("la venta anónima no debe contarse: suma=%d, esperaba 26000", res.Redeemed.SpendTotal+res.NotRedeemed.SpendTotal)
	}
}

// Sin canjes en el rango => redeemed.customers:0, columna "no canjeó" intacta.
func TestLoyaltyEffectNoRedemptions(t *testing.T) {
	w := newWorld(t)
	defer w.close()
	a := w.tenant("A")
	now := time.Now()
	from := now.AddDate(0, 0, -30)
	to := now.AddDate(0, 0, 1)

	cn := w.customer(a, "222")
	w.sale(a, &cn, nil, 4000, now.AddDate(0, 0, -10))
	w.sale(a, &cn, nil, 6000, now.AddDate(0, 0, -5))

	res, err := w.svc.LoyaltyEffect(w.ctx, Scope{TenantID: a, From: from, To: to})
	if err != nil {
		t.Fatalf("loyalty-effect: %v", err)
	}
	if res.Redeemed.Customers != 0 || res.Redeemed.SalesTotal != 0 {
		t.Fatalf("sin canjes: esperaba redeemed 0/0, obtuvo %+v", res.Redeemed)
	}
	if res.NotRedeemed.Customers != 1 || res.NotRedeemed.SalesTotal != 2 || res.NotRedeemed.SpendTotal != 10000 {
		t.Fatalf("no canjeó intacto: esperaba 1/2/10000, obtuvo %+v", res.NotRedeemed)
	}
}

// Regresión de esquema (ADR-009 R1 / tech-spec §8). La columna sales.loyalty_reward
// fue ELIMINADA por la migración 0010_loyalty_promotions; Insight 6 debe leer el
// canje desde loyalty_redemptions. Este test falla explícitamente si:
//  1. alguien reintroduce la columna sales.loyalty_reward en el esquema, o
//  2. el store vuelve a referenciar loyalty_reward como fuente.
// Previene el bug ya detectado y corregido (no volver a la letra del PRD/handoff).
func TestLoyaltyEffectSchemaRegression(t *testing.T) {
	w := newWorld(t)
	defer w.close()

	var exists bool
	if err := w.pool.QueryRow(w.ctx, `SELECT EXISTS(
		SELECT 1 FROM information_schema.columns
		WHERE table_name = 'sales' AND column_name = 'loyalty_reward')`).Scan(&exists); err != nil {
		t.Fatalf("consulta de esquema: %v", err)
	}
	if exists {
		t.Fatal("REGRESIÓN: la columna sales.loyalty_reward reapareció en el esquema. " +
			"La borró 0010_loyalty_promotions; Insight 6 se calcula desde loyalty_redemptions (ADR-009 R1). " +
			"No reintroducirla como fuente de lealtad.")
	}

	// Guarda complementaria a nivel de código: el store NO debe consultar la columna
	// sales.loyalty_reward como fuente (p. ej. `s.loyalty_reward`, `sales.loyalty_reward`
	// o `FROM ... loyalty_reward`). Se ignoran las líneas de comentario, que SÍ mencionan
	// la columna a propósito para documentar por qué NO se usa (ADR-009 R1).
	src, err := os.ReadFile("store.go")
	if err != nil {
		t.Fatalf("leer store.go: %v", err)
	}
	for i, line := range bytes.Split(src, []byte("\n")) {
		code := line
		if idx := bytes.Index(code, []byte("//")); idx >= 0 {
			code = code[:idx] // descarta el comentario de la línea
		}
		if bytes.Contains(code, []byte("loyalty_reward")) {
			t.Fatalf("REGRESIÓN (store.go:%d): el código referencia loyalty_reward. "+
				"La fuente de Insight 6 es la tabla loyalty_redemptions, no sales.loyalty_reward (ADR-009 R1).", i+1)
		}
	}
}

// ============================================================================
// Q4 — Transversal: determinismo, aislamiento por tenant, estados vacíos
// ============================================================================

// seedRich siembra un escenario razonablemente completo para un tenant dado, que
// ejercita las 6 queries (identificados, anónimas, receta, redención, pares).
func seedRich(w *world, tenant string, phonePrefix string) (from, to time.Time) {
	now := time.Now()
	from = now.AddDate(0, 0, -30)
	to = now.AddDate(0, 0, 1)

	latte := w.product(tenant, "Latte", 5000)
	muffin := w.product(tenant, "Muffin", 3000)
	leche := w.supply(tenant, "Leche", 1000, ptr(2000))
	w.recipe(tenant, latte, leche, 200)

	c1 := w.customer(tenant, phonePrefix+"1")
	c2 := w.customer(tenant, phonePrefix+"2")
	for i := 0; i < 3; i++ {
		s := w.sale(tenant, &c1, nil, 8000, now.AddDate(0, 0, -20+i*4))
		w.item(s, &latte, "Latte", 5000, 1)
		w.item(s, &muffin, "Muffin", 3000, 1)
		if i == 2 {
			w.redeem(tenant, c1, s)
		}
	}
	for i := 0; i < 2; i++ {
		s := w.sale(tenant, &c2, nil, 8000, now.AddDate(0, 0, -15+i*5))
		w.item(s, &latte, "Latte", 5000, 1)
		w.item(s, &muffin, "Muffin", 3000, 1)
	}
	sa := w.sale(tenant, nil, nil, 2000, now.AddDate(0, 0, -3))
	w.item(sa, &muffin, "Muffin", 3000, 1)
	return from, to
}

// callAll ejecuta los 6 insights y devuelve su JSON concatenado (para comparar).
func callAll(t *testing.T, svc *Service, sc Scope) []byte {
	t.Helper()
	ctx := context.Background()
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	r1, err := svc.Recurrence(ctx, sc)
	must(t, err)
	must(t, enc.Encode(r1))
	r2, err := svc.TopProducts(ctx, sc)
	must(t, err)
	must(t, enc.Encode(r2))
	r3, err := svc.TicketSegments(ctx, sc)
	must(t, err)
	must(t, enc.Encode(r3))
	r4, err := svc.SecondVisit(ctx, sc)
	must(t, err)
	must(t, enc.Encode(r4))
	r5, err := svc.BasketAffinity(ctx, sc)
	must(t, err)
	must(t, enc.Encode(r5))
	r6, err := svc.LoyaltyEffect(ctx, sc)
	must(t, err)
	must(t, enc.Encode(r6))
	return buf.Bytes()
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("insight: %v", err)
	}
}

// Determinismo: la misma request dos veces produce EXACTAMENTE el mismo JSON
// (sin IA, sin aleatoriedad).
func TestDeterminism(t *testing.T) {
	w := newWorld(t)
	defer w.close()
	a := w.tenant("A")
	from, to := seedRich(w, a, "5")
	sc := Scope{TenantID: a, From: from, To: to}

	first := callAll(t, w.svc, sc)
	second := callAll(t, w.svc, sc)
	if !bytes.Equal(first, second) {
		t.Fatalf("no determinista:\n1: %s\n2: %s", first, second)
	}
}

// Aislamiento por tenant: los datos del tenant B nunca aparecen en los resultados
// del tenant A, en las 6 queries.
func TestTenantIsolation(t *testing.T) {
	w := newWorld(t)
	defer w.close()
	a := w.tenant("A")
	b := w.tenant("B")
	fromA, toA := seedRich(w, a, "5")
	seedRich(w, b, "9") // ruido del otro tenant

	scA := Scope{TenantID: a, From: fromA, To: toA}

	// Recurrencia: solo 2 identificados de A (no 4 de A+B).
	rec, err := w.svc.Recurrence(w.ctx, scA)
	if err != nil {
		t.Fatalf("recurrence: %v", err)
	}
	if rec.WithGe1 != 2 {
		t.Fatalf("aislamiento recurrencia: esperaba 2 identificados de A, obtuvo %d", rec.WithGe1)
	}
	if rec.TotalSales != 6 {
		t.Fatalf("aislamiento ventas: esperaba 6 de A, obtuvo %d", rec.TotalSales)
	}

	// Ticket: identificadas de A = 5 (3+2), anónimas 1.
	ts, err := w.svc.TicketSegments(w.ctx, scA)
	if err != nil {
		t.Fatalf("ticket-segments: %v", err)
	}
	if ts.New.Sales+ts.Recurring.Sales != 5 || ts.Anonymous.Sales != 1 {
		t.Fatalf("aislamiento ticket: esperaba 5 identificadas + 1 anónima de A, obtuvo %+v/%+v/%+v", ts.New, ts.Recurring, ts.Anonymous)
	}

	// Lealtad: 1 cliente canjeó (A), 1 no. Nada de B.
	le, err := w.svc.LoyaltyEffect(w.ctx, scA)
	if err != nil {
		t.Fatalf("loyalty-effect: %v", err)
	}
	if le.Redeemed.Customers != 1 || le.NotRedeemed.Customers != 1 {
		t.Fatalf("aislamiento lealtad: esperaba 1/1 de A, obtuvo %+v/%+v", le.Redeemed, le.NotRedeemed)
	}

	// Top-products: solo Latte/Muffin de A (2 productos), no los de B.
	tp, err := w.svc.TopProducts(w.ctx, scA)
	if err != nil {
		t.Fatalf("top-products: %v", err)
	}
	if len(tp.ByRevenue) != 2 {
		t.Fatalf("aislamiento productos: esperaba 2 productos de A, obtuvo %+v", tp.ByRevenue)
	}

	// Afinidad: el par Latte+Muffin de A (soporte 5), no mezclado con B.
	ba, err := w.svc.BasketAffinity(w.ctx, scA)
	if err != nil {
		t.Fatalf("basket-affinity: %v", err)
	}
	if len(ba.Items) != 1 || ba.Items[0].Support != 5 {
		t.Fatalf("aislamiento afinidad: esperaba 1 par soporte 5 de A, obtuvo %+v", ba.Items)
	}

	// Second-visit de A: 2 segundas visitas (< 5) => insuficiente, sin datos de B.
	sv, err := w.svc.SecondVisit(w.ctx, scA)
	if err != nil {
		t.Fatalf("second-visit: %v", err)
	}
	if sv.SampleSize != 2 {
		t.Fatalf("aislamiento 2ª visita: esperaba sampleSize 2 de A, obtuvo %d", sv.SampleSize)
	}
}

// Estados vacíos: con un tenant sin ventas, los 6 insights devuelven sin error un
// cuerpo válido (el handler lo traduce a 200, no 500; habilita la carga aislada).
func TestEmptyStatesNoError(t *testing.T) {
	w := newWorld(t)
	defer w.close()
	a := w.tenant("A") // sin ninguna venta
	now := time.Now()
	sc := Scope{TenantID: a, From: now.AddDate(0, 0, -30), To: now.AddDate(0, 0, 1)}

	rec, err := w.svc.Recurrence(w.ctx, sc)
	if err != nil {
		t.Fatalf("recurrence vacío: %v", err)
	}
	if !rec.Empty || rec.TotalSales != 0 {
		t.Fatalf("recurrence vacío: esperaba empty=true totalSales=0, obtuvo %+v", rec)
	}
	tp, err := w.svc.TopProducts(w.ctx, sc)
	if err != nil {
		t.Fatalf("top-products vacío: %v", err)
	}
	if len(tp.ByRevenue) != 0 || len(tp.ByMargin) != 0 || len(tp.ExcludedFromMargin) != 0 {
		t.Fatalf("top-products vacío: esperaba listas vacías (no nil), obtuvo %+v", tp)
	}
	ts, err := w.svc.TicketSegments(w.ctx, sc)
	if err != nil {
		t.Fatalf("ticket-segments vacío: %v", err)
	}
	if ts.New.Sales != 0 || ts.Recurring.Sales != 0 || ts.Anonymous.Sales != 0 {
		t.Fatalf("ticket-segments vacío: esperaba todo 0, obtuvo %+v", ts)
	}
	sv, err := w.svc.SecondVisit(w.ctx, sc)
	if err != nil {
		t.Fatalf("second-visit vacío: %v", err)
	}
	if !sv.Insufficient || len(sv.Items) != 0 {
		t.Fatalf("second-visit vacío: esperaba insufficient sin items, obtuvo %+v", sv)
	}
	ba, err := w.svc.BasketAffinity(w.ctx, sc)
	if err != nil {
		t.Fatalf("basket-affinity vacío: %v", err)
	}
	if !ba.Insufficient || len(ba.Items) != 0 {
		t.Fatalf("basket-affinity vacío: esperaba insufficient sin items, obtuvo %+v", ba)
	}
	le, err := w.svc.LoyaltyEffect(w.ctx, sc)
	if err != nil {
		t.Fatalf("loyalty-effect vacío: %v", err)
	}
	if le.Redeemed.Customers != 0 || le.NotRedeemed.Customers != 0 {
		t.Fatalf("loyalty-effect vacío: esperaba 0/0 clientes, obtuvo %+v", le)
	}
}
