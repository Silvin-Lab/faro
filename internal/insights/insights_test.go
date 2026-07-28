package insights

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// testEnv siembra un negocio con datos mínimos suficientes para ejercitar el
// camino feliz de los 6 insights (shape correcto del struct). La matriz completa
// de casos borde la cubren las tareas de QA (Q1–Q4), no se duplica aquí.
//
// Devuelve el service, el pool, el tenant A (con datos) y el rango [from,to).
func testEnv(t *testing.T) (*Service, *pgxpool.Pool, string, time.Time, time.Time) {
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
		"TRUNCATE loyalty_redemptions, loyalty_promotion_products, loyalty_promotions, product_supplies, supply_measures, supplies, supply_categories, sale_items, sales, customers, products, categories, users, branches, tenants RESTART IDENTITY CASCADE"); err != nil {
		t.Fatalf("truncate: %v", err)
	}

	q := func(dst *string, sql string, args ...any) {
		if err := pool.QueryRow(ctx, sql, args...).Scan(dst); err != nil {
			t.Fatalf("seed (%s): %v", sql, err)
		}
	}
	exec := func(sql string, args ...any) {
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("seed exec (%s): %v", sql, err)
		}
	}

	var a, cat, latte, muffin, agua string
	q(&a, "INSERT INTO tenants (name) VALUES ('A') RETURNING id::text")
	q(&cat, "INSERT INTO categories (tenant_id, name) VALUES ($1,'Bebidas') RETURNING id::text", a)
	q(&latte, "INSERT INTO products (tenant_id, category_id, name, price_cents) VALUES ($1,$2,'Latte',5000) RETURNING id::text", a, cat)
	q(&muffin, "INSERT INTO products (tenant_id, category_id, name, price_cents) VALUES ($1,$2,'Muffin',3000) RETURNING id::text", a, cat)
	q(&agua, "INSERT INTO products (tenant_id, category_id, name, price_cents) VALUES ($1,$2,'Agua',2000) RETURNING id::text", a, cat)

	// Receta del Latte: leche con costo capturado. Muffin sin receta (excluido del
	// margen). Agua con un insumo sin costo (has_null_cost => excluido del margen).
	var leche, cacao string
	q(&leche, "INSERT INTO supplies (tenant_id, name, base_unit, package_name, package_content, package_cost_cents) VALUES ($1,'Leche','ml','Litro',1000,2000) RETURNING id::text", a)
	q(&cacao, "INSERT INTO supplies (tenant_id, name, base_unit, package_name, package_content, package_cost_cents) VALUES ($1,'Cacao','g','Bolsa',500,NULL) RETURNING id::text", a)
	exec("INSERT INTO product_supplies (tenant_id, product_id, supply_id, quantity_base) VALUES ($1,$2,$3,200)", a, latte, leche)
	exec("INSERT INTO product_supplies (tenant_id, product_id, supply_id, quantity_base) VALUES ($1,$2,$3,10)", a, agua, cacao)

	// Dos clientes identificados.
	var cA, cB string
	q(&cA, "INSERT INTO customers (tenant_id, phone, first_name, last_name) VALUES ($1,'111','Ana','Perez') RETURNING id::text", a)
	q(&cB, "INSERT INTO customers (tenant_id, phone, first_name, last_name) VALUES ($1,'222','Beto','Gil') RETURNING id::text", a)

	now := time.Now()
	sale := func(customer *string, total int, at time.Time) string {
		var id string
		if err := pool.QueryRow(ctx,
			"INSERT INTO sales (tenant_id, customer_id, total_cents, amount_paid_cents, change_cents, payment_method, created_at) VALUES ($1,$2,$3,$3,0,'cash',$4) RETURNING id::text",
			a, customer, total, at).Scan(&id); err != nil {
			t.Fatalf("seed venta: %v", err)
		}
		return id
	}
	item := func(saleID, prod, name string, price, qty int) {
		exec("INSERT INTO sale_items (sale_id, product_id, name, unit_price_cents, quantity, line_total_cents) VALUES ($1,$2,$3,$4,$5,$6)",
			saleID, prod, name, price, qty, price*qty)
	}

	// Ana (cB): recurrente (3 ventas), canjea lealtad. Latte+Muffin juntos (par).
	s1 := sale(&cA, 8000, now.AddDate(0, 0, -20))
	item(s1, latte, "Latte", 5000, 1)
	item(s1, muffin, "Muffin", 3000, 1)
	s2 := sale(&cA, 8000, now.AddDate(0, 0, -10)) // 2ª visita
	item(s2, latte, "Latte", 5000, 1)
	item(s2, muffin, "Muffin", 3000, 1)
	s3 := sale(&cA, 5000, now.AddDate(0, 0, -5))
	item(s3, latte, "Latte", 5000, 1)

	// Beto (cB): recurrente (2 ventas), no canjea. Latte+Muffin juntos (par).
	s4 := sale(&cB, 8000, now.AddDate(0, 0, -15))
	item(s4, latte, "Latte", 5000, 1)
	item(s4, muffin, "Muffin", 3000, 1)
	s5 := sale(&cB, 8000, now.AddDate(0, 0, -8)) // 2ª visita
	item(s5, latte, "Latte", 5000, 1)
	item(s5, muffin, "Muffin", 3000, 1)

	// Venta anónima (customer_id NULL) con Agua.
	s6 := sale(nil, 2000, now.AddDate(0, 0, -3))
	item(s6, agua, "Agua", 2000, 1)

	// Canje de lealtad de Ana sobre s3.
	exec(`INSERT INTO loyalty_redemptions
		(tenant_id, customer_id, sale_id, promotion_name, discount_percent, visit_threshold, caused_reset, visits_cycle_at, visits_lifetime_at, discount_cents)
		VALUES ($1,$2,$3,'Descuento',10,3,false,3,3,500)`, a, cA, s3)

	from := now.AddDate(0, 0, -30)
	to := now.AddDate(0, 0, 1)
	return NewService(pool), pool, a, from, to
}

func TestRecurrence(t *testing.T) {
	svc, pool, a, from, to := testEnv(t)
	defer pool.Close()
	sc := Scope{TenantID: a, From: from, To: to}

	res, err := svc.Recurrence(context.Background(), sc)
	if err != nil {
		t.Fatalf("recurrence: %v", err)
	}
	// 2 identificados, ambos recurrentes (n>=2) => tasa 100%.
	if res.WithGe1 != 2 || res.WithGe2 != 2 {
		t.Fatalf("identificados: esperaba 2/2, obtuvo %d/%d", res.WithGe1, res.WithGe2)
	}
	if res.RatePct != 100 {
		t.Fatalf("ratePct: esperaba 100, obtuvo %v", res.RatePct)
	}
	if res.Empty {
		t.Fatal("empty: esperaba false con identificados")
	}
	// 6 ventas totales (3 Ana + 2 Beto + 1 anónima), 1 anónima.
	if res.AnonSales != 1 || res.TotalSales != 6 {
		t.Fatalf("anónimas: esperaba 1/6, obtuvo %d/%d", res.AnonSales, res.TotalSales)
	}
	if res.Cohort.N != 2 {
		t.Fatalf("cohorte: esperaba n=2, obtuvo %d", res.Cohort.N)
	}
}

func TestTopProducts(t *testing.T) {
	svc, pool, a, from, to := testEnv(t)
	defer pool.Close()
	sc := Scope{TenantID: a, From: from, To: to}

	res, err := svc.TopProducts(context.Background(), sc)
	if err != nil {
		t.Fatalf("top-products: %v", err)
	}
	if len(res.ByRevenue) == 0 || len(res.ByVolume) == 0 {
		t.Fatalf("rankings vacíos: rev=%d vol=%d", len(res.ByRevenue), len(res.ByVolume))
	}
	// Latte es el mayor ingreso (5 unidades × 5000).
	if res.ByRevenue[0].Name != "Latte" {
		t.Fatalf("ingresos: esperaba Latte primero, obtuvo %s", res.ByRevenue[0].Name)
	}
	// Latte tiene receta con costo => aparece en margen. Muffin (sin receta) y Agua
	// (insumo sin costo) van a excluidos.
	foundLatteMargin := false
	for _, m := range res.ByMargin {
		if m.Name == "Latte" {
			foundLatteMargin = true
		}
	}
	if !foundLatteMargin {
		t.Fatalf("margen: esperaba Latte, obtuvo %+v", res.ByMargin)
	}
	reasons := map[string]string{}
	for _, e := range res.ExcludedFromMargin {
		reasons[e.Name] = e.Reason
	}
	if reasons["Muffin"] != "no_recipe" {
		t.Fatalf("excluidos: Muffin esperaba no_recipe, obtuvo %q", reasons["Muffin"])
	}
	if reasons["Agua"] != "null_cost" {
		t.Fatalf("excluidos: Agua esperaba null_cost, obtuvo %q", reasons["Agua"])
	}
	// Rankings por categoría (addendum §1): todo el seed vive en "Bebidas".
	if len(res.ByCategoryRevenue) == 0 || len(res.ByCategoryVolume) == 0 {
		t.Fatalf("rankings categoría vacíos: rev=%d vol=%d", len(res.ByCategoryRevenue), len(res.ByCategoryVolume))
	}
	if res.ByCategoryRevenue[0].Name != "Bebidas" {
		t.Fatalf("categoría ingresos: esperaba Bebidas primero, obtuvo %s", res.ByCategoryRevenue[0].Name)
	}
	// Ingresos de Bebidas = 5 Latte×5000 + 4 Muffin×3000 + 1 Agua×2000 = 39000.
	if res.ByCategoryRevenue[0].RevenueCents != 39000 {
		t.Fatalf("categoría ingresos: esperaba 39000, obtuvo %d", res.ByCategoryRevenue[0].RevenueCents)
	}
	// Volumen de Bebidas = 5 + 4 + 1 = 10 unidades.
	if res.ByCategoryVolume[0].Units != 10 {
		t.Fatalf("categoría volumen: esperaba 10 unidades, obtuvo %d", res.ByCategoryVolume[0].Units)
	}
}

func TestTicketSegments(t *testing.T) {
	svc, pool, a, from, to := testEnv(t)
	defer pool.Close()
	sc := Scope{TenantID: a, From: from, To: to}

	res, err := svc.TicketSegments(context.Background(), sc)
	if err != nil {
		t.Fatalf("ticket-segments: %v", err)
	}
	// Ambos clientes son recurrentes => nuevo tiene 0 ventas, recurrente 5.
	if res.New.Sales != 0 || res.New.AvgCents != 0 {
		t.Fatalf("nuevo: esperaba 0/0, obtuvo %+v", res.New)
	}
	if res.Recurring.Sales != 5 {
		t.Fatalf("recurrente: esperaba 5 ventas, obtuvo %d", res.Recurring.Sales)
	}
	// Anónimas: 1 venta de 2000.
	if res.Anonymous.Sales != 1 || res.Anonymous.AvgCents != 2000 {
		t.Fatalf("anónimas: esperaba 1/2000, obtuvo %+v", res.Anonymous)
	}
}

func TestSecondVisit(t *testing.T) {
	svc, pool, a, from, to := testEnv(t)
	defer pool.Close()
	sc := Scope{TenantID: a, From: from, To: to}

	res, err := svc.SecondVisit(context.Background(), sc)
	if err != nil {
		t.Fatalf("second-visit: %v", err)
	}
	// Solo 2 clientes tienen 2ª visita (< default 5) => insuficiente sin lista.
	if !res.Insufficient {
		t.Fatalf("esperaba insufficient=true con muestra pequeña, obtuvo %+v", res)
	}
	if res.SampleSize != 2 {
		t.Fatalf("sampleSize: esperaba 2, obtuvo %d", res.SampleSize)
	}
	if len(res.Items) != 0 {
		t.Fatalf("items: esperaba lista vacía en insuficiente, obtuvo %d", len(res.Items))
	}
}

func TestBasketAffinity(t *testing.T) {
	svc, pool, a, from, to := testEnv(t)
	defer pool.Close()
	sc := Scope{TenantID: a, From: from, To: to}

	res, err := svc.BasketAffinity(context.Background(), sc)
	if err != nil {
		t.Fatalf("basket-affinity: %v", err)
	}
	// Latte+Muffin aparecen juntos en 4 ventas (s1,s2,s4,s5; >= default 3) => un par.
	if res.Insufficient {
		t.Fatalf("esperaba suficiente con par de soporte 4, obtuvo insufficient")
	}
	if len(res.Items) != 1 {
		t.Fatalf("items: esperaba 1 par, obtuvo %d (%+v)", len(res.Items), res.Items)
	}
	p := res.Items[0]
	if p.Support != 4 {
		t.Fatalf("support: esperaba 4, obtuvo %d", p.Support)
	}
	if p.Lift <= 0 {
		t.Fatalf("lift: esperaba > 0, obtuvo %v", p.Lift)
	}
	// Afinidad por categoría (addendum §2): el seed tiene una sola categoría
	// ("Bebidas"), que nunca produce un par → categoryInsufficient=true (§2.3).
	if !res.CategoryInsufficient {
		t.Fatalf("categoría: esperaba categoryInsufficient=true con una sola categoría, obtuvo %+v", res.CategoryItems)
	}
	if len(res.CategoryItems) != 0 {
		t.Fatalf("categoría: esperaba lista vacía, obtuvo %d", len(res.CategoryItems))
	}
}

// catTestEnv siembra un negocio con VARIAS categorías, un producto sin categoría
// y una línea de producto borrado, para ejercitar los rankings y la afinidad por
// categoría del addendum (bucket "Sin categoría", exclusión de self-par).
func catTestEnv(t *testing.T) (*Service, *pgxpool.Pool, string, time.Time, time.Time) {
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
		"TRUNCATE loyalty_redemptions, loyalty_promotion_products, loyalty_promotions, product_supplies, supply_measures, supplies, supply_categories, sale_items, sales, customers, products, categories, users, branches, tenants RESTART IDENTITY CASCADE"); err != nil {
		t.Fatalf("truncate: %v", err)
	}

	q := func(dst *string, sql string, args ...any) {
		if err := pool.QueryRow(ctx, sql, args...).Scan(dst); err != nil {
			t.Fatalf("seed (%s): %v", sql, err)
		}
	}
	exec := func(sql string, args ...any) {
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("seed exec (%s): %v", sql, err)
		}
	}

	var a, catPan, catCafe, croissant, medialuna, espresso, galleta string
	q(&a, "INSERT INTO tenants (name) VALUES ('A') RETURNING id::text")
	q(&catPan, "INSERT INTO categories (tenant_id, name) VALUES ($1,'Panadería') RETURNING id::text", a)
	q(&catCafe, "INSERT INTO categories (tenant_id, name) VALUES ($1,'Café') RETURNING id::text", a)
	q(&croissant, "INSERT INTO products (tenant_id, category_id, name, price_cents) VALUES ($1,$2,'Croissant',3000) RETURNING id::text", a, catPan)
	q(&medialuna, "INSERT INTO products (tenant_id, category_id, name, price_cents) VALUES ($1,$2,'Medialuna',2500) RETURNING id::text", a, catPan)
	q(&espresso, "INSERT INTO products (tenant_id, category_id, name, price_cents) VALUES ($1,$2,'Espresso',4000) RETURNING id::text", a, catCafe)
	// Producto vivo SIN categoría (category_id NULL) → bucket "Sin categoría".
	q(&galleta, "INSERT INTO products (tenant_id, category_id, name, price_cents) VALUES ($1,NULL,'Galleta',1500) RETURNING id::text", a)

	now := time.Now()
	sale := func(total int, at time.Time) string {
		var id string
		if err := pool.QueryRow(ctx,
			"INSERT INTO sales (tenant_id, customer_id, total_cents, amount_paid_cents, change_cents, payment_method, created_at) VALUES ($1,NULL,$2,$2,0,'cash',$3) RETURNING id::text",
			a, total, at).Scan(&id); err != nil {
			t.Fatalf("seed venta: %v", err)
		}
		return id
	}
	item := func(saleID, prod, name string, price, qty int) {
		exec("INSERT INTO sale_items (sale_id, product_id, name, unit_price_cents, quantity, line_total_cents) VALUES ($1,$2,$3,$4,$5,$6)",
			saleID, prod, name, price, qty, price*qty)
	}
	// Línea de producto borrado: product_id NULL → sin categoría recuperable (§1.2).
	deletedItem := func(saleID, name string, price, qty int) {
		exec("INSERT INTO sale_items (sale_id, product_id, name, unit_price_cents, quantity, line_total_cents) VALUES ($1,NULL,$2,$3,$4,$5)",
			saleID, name, price, qty, price*qty)
	}

	// Pares Café–Panadería con soporte 3 (X1,X2,X3).
	x1 := sale(7000, now.AddDate(0, 0, -20))
	item(x1, croissant, "Croissant", 3000, 1)
	item(x1, espresso, "Espresso", 4000, 1)
	x2 := sale(7000, now.AddDate(0, 0, -18))
	item(x2, croissant, "Croissant", 3000, 1)
	item(x2, espresso, "Espresso", 4000, 1)
	x3 := sale(6500, now.AddDate(0, 0, -16))
	item(x3, medialuna, "Medialuna", 2500, 1)
	item(x3, espresso, "Espresso", 4000, 1)
	// X4: dos productos de la MISMA categoría (Panadería) → NO debe generar self-par.
	x4 := sale(5500, now.AddDate(0, 0, -14))
	item(x4, croissant, "Croissant", 3000, 1)
	item(x4, medialuna, "Medialuna", 2500, 1)
	// X5: producto sin categoría + producto borrado → ambos al bucket "Sin categoría".
	x5 := sale(2500, now.AddDate(0, 0, -12))
	item(x5, galleta, "Galleta", 1500, 1)
	deletedItem(x5, "Producto viejo", 1000, 1)

	from := now.AddDate(0, 0, -30)
	to := now.AddDate(0, 0, 1)
	return NewService(pool), pool, a, from, to
}

func TestTopProductsByCategory(t *testing.T) {
	svc, pool, a, from, to := catTestEnv(t)
	defer pool.Close()
	sc := Scope{TenantID: a, From: from, To: to}

	res, err := svc.TopProducts(context.Background(), sc)
	if err != nil {
		t.Fatalf("top-products: %v", err)
	}
	rev := map[string]CategoryRevenue{}
	for _, c := range res.ByCategoryRevenue {
		rev[c.Name] = c
	}
	// Panadería: 3 Croissant×3000 + 2 Medialuna×2500 = 14000, 5 unidades.
	if rev["Panadería"].RevenueCents != 14000 || rev["Panadería"].Units != 5 {
		t.Fatalf("Panadería: esperaba 14000/5, obtuvo %+v", rev["Panadería"])
	}
	// Café: 3 Espresso×4000 = 12000, 3 unidades.
	if rev["Café"].RevenueCents != 12000 || rev["Café"].Units != 3 {
		t.Fatalf("Café: esperaba 12000/3, obtuvo %+v", rev["Café"])
	}
	// "Sin categoría": Galleta 1500 (vivo sin cat) + Producto viejo 1000 (borrado) = 2500.
	if rev["Sin categoría"].RevenueCents != 2500 || rev["Sin categoría"].Units != 2 {
		t.Fatalf("Sin categoría: esperaba 2500/2 (galleta + borrado), obtuvo %+v", rev["Sin categoría"])
	}
	// Orden por ingresos: Panadería (14000) primero.
	if res.ByCategoryRevenue[0].Name != "Panadería" {
		t.Fatalf("orden ingresos: esperaba Panadería primero, obtuvo %s", res.ByCategoryRevenue[0].Name)
	}
}

func TestBasketAffinityByCategory(t *testing.T) {
	svc, pool, a, from, to := catTestEnv(t)
	defer pool.Close()
	sc := Scope{TenantID: a, From: from, To: to}

	res, err := svc.BasketAffinity(context.Background(), sc)
	if err != nil {
		t.Fatalf("basket-affinity: %v", err)
	}
	if res.CategoryInsufficient {
		t.Fatalf("categoría: esperaba suficiente con par Café–Panadería, obtuvo insuficiente")
	}
	// Único par esperado: Café–Panadería con soporte 3 (X1,X2,X3). X4 (2 productos
	// de Panadería) NO debe aportar un self-par Panadería–Panadería (§2.1). X5 (solo
	// "Sin categoría") tampoco genera par.
	if len(res.CategoryItems) != 1 {
		t.Fatalf("categoría: esperaba 1 par, obtuvo %d (%+v)", len(res.CategoryItems), res.CategoryItems)
	}
	cp := res.CategoryItems[0]
	names := map[string]bool{cp.A: true, cp.B: true}
	if !names["Café"] || !names["Panadería"] {
		t.Fatalf("categoría: esperaba par {Café, Panadería}, obtuvo {%s, %s}", cp.A, cp.B)
	}
	if names["Sin categoría"] {
		t.Fatalf("categoría: no debía aparecer 'Sin categoría' en el par, obtuvo {%s, %s}", cp.A, cp.B)
	}
	if cp.Support != 3 {
		t.Fatalf("categoría support: esperaba 3, obtuvo %d", cp.Support)
	}
	if cp.Lift <= 0 {
		t.Fatalf("categoría lift: esperaba > 0, obtuvo %v", cp.Lift)
	}
}

func TestLoyaltyEffect(t *testing.T) {
	svc, pool, a, from, to := testEnv(t)
	defer pool.Close()
	sc := Scope{TenantID: a, From: from, To: to}

	res, err := svc.LoyaltyEffect(context.Background(), sc)
	if err != nil {
		t.Fatalf("loyalty-effect: %v", err)
	}
	// Ana canjeó (1 cliente, 3 ventas). Beto no (1 cliente, 2 ventas).
	if res.Redeemed.Customers != 1 || res.Redeemed.SalesTotal != 3 {
		t.Fatalf("canjeó: esperaba 1 cliente / 3 ventas, obtuvo %+v", res.Redeemed)
	}
	if res.NotRedeemed.Customers != 1 || res.NotRedeemed.SalesTotal != 2 {
		t.Fatalf("no canjeó: esperaba 1 cliente / 2 ventas, obtuvo %+v", res.NotRedeemed)
	}
	// Derivados con guarda: gasto/cliente de Ana = 21000, de Beto = 16000.
	if res.Redeemed.SpendPerCustomer != 21000 {
		t.Fatalf("gasto/cliente canjeó: esperaba 21000, obtuvo %v", res.Redeemed.SpendPerCustomer)
	}
}
