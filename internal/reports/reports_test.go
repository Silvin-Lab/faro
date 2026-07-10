package reports

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func testSvc(t *testing.T) (*Service, *pgxpool.Pool, string, string) {
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
	if _, err := pool.Exec(ctx, "TRUNCATE expenses, expense_concepts, expense_categories, sale_items, sales, products, categories, customers, users, branches, tenants RESTART IDENTITY CASCADE"); err != nil {
		t.Fatalf("truncate: %v", err)
	}

	var a, b, cat, prod string
	pool.QueryRow(ctx, "INSERT INTO tenants (name) VALUES ('A') RETURNING id::text").Scan(&a)
	pool.QueryRow(ctx, "INSERT INTO tenants (name) VALUES ('B') RETURNING id::text").Scan(&b)
	pool.QueryRow(ctx, "INSERT INTO categories (tenant_id, name) VALUES ($1,'Bebidas') RETURNING id::text", a).Scan(&cat)
	pool.QueryRow(ctx, "INSERT INTO products (tenant_id, category_id, name, price_cents) VALUES ($1,$2,'Latte',5000) RETURNING id::text", a, cat).Scan(&prod)

	// Negocio A: dos ventas (efectivo 2×, tarjeta 1×).
	var s1, s2 string
	pool.QueryRow(ctx, "INSERT INTO sales (tenant_id,total_cents,amount_paid_cents,change_cents,payment_method) VALUES ($1,10000,10000,0,'cash') RETURNING id::text", a).Scan(&s1)
	pool.Exec(ctx, "INSERT INTO sale_items (sale_id,product_id,name,unit_price_cents,quantity,line_total_cents) VALUES ($1,$2,'Latte',5000,2,10000)", s1, prod)
	pool.QueryRow(ctx, "INSERT INTO sales (tenant_id,total_cents,amount_paid_cents,change_cents,payment_method) VALUES ($1,5000,5000,0,'card') RETURNING id::text", a).Scan(&s2)
	pool.Exec(ctx, "INSERT INTO sale_items (sale_id,product_id,name,unit_price_cents,quantity,line_total_cents) VALUES ($1,$2,'Latte',5000,1,5000)", s2, prod)

	// Negocio B: una venta sin items (para aislamiento).
	pool.Exec(ctx, "INSERT INTO sales (tenant_id,total_cents,amount_paid_cents,change_cents,payment_method) VALUES ($1,9999,9999,0,'cash')", b)

	return NewService(pool), pool, a, b
}

func TestSalesReport(t *testing.T) {
	svc, pool, a, b := testSvc(t)
	defer pool.Close()
	ctx := context.Background()
	from := time.Now().Add(-time.Hour)
	to := time.Now().Add(time.Hour)

	rep, err := svc.SalesReport(ctx, a, from, to, 0, BranchFilter{})
	if err != nil {
		t.Fatalf("reporte: %v", err)
	}
	if rep.TotalCents != 15000 || rep.SalesCount != 2 {
		t.Fatalf("resumen: esperaba total=15000 count=2, obtuvo total=%d count=%d", rep.TotalCents, rep.SalesCount)
	}

	// Por forma de pago: dos métodos, suma 15000.
	pmTotal := 0
	for _, p := range rep.ByPaymentMethod {
		pmTotal += p.TotalCents
	}
	if len(rep.ByPaymentMethod) != 2 || pmTotal != 15000 {
		t.Fatalf("por pago: %+v", rep.ByPaymentMethod)
	}

	// Por categoría: una categoría, 3 unidades, 15000.
	if len(rep.ByCategory) != 1 || rep.ByCategory[0].CategoryName != "Bebidas" ||
		rep.ByCategory[0].Quantity != 3 || rep.ByCategory[0].TotalCents != 15000 {
		t.Fatalf("por categoría: %+v", rep.ByCategory)
	}

	// Por hora: al menos una franja.
	if len(rep.ByHour) == 0 {
		t.Fatal("por hora: esperaba al menos una franja")
	}

	// Aislamiento: el negocio B solo ve su venta (9999, sin items).
	repB, err := svc.SalesReport(ctx, b, from, to, 0, BranchFilter{})
	if err != nil {
		t.Fatalf("reporte B: %v", err)
	}
	if repB.TotalCents != 9999 || repB.SalesCount != 1 || len(repB.ByCategory) != 0 {
		t.Fatalf("aislamiento B: total=%d count=%d categorías=%d", repB.TotalCents, repB.SalesCount, len(repB.ByCategory))
	}
}

// TestExpensesReport cubre totales, bucket "Sin categoría", desglose por sucursal,
// filtro BranchFilter y aislamiento de tenant.
func TestExpensesReport(t *testing.T) {
	svc, pool, a, b := testSvc(t)
	defer pool.Close()
	ctx := context.Background()

	var bA1, bA2, bB1, userA, userB, catServ, conLuz, conVarios, conB string
	q := func(dst *string, sql string, args ...any) {
		if err := pool.QueryRow(ctx, sql, args...).Scan(dst); err != nil {
			t.Fatalf("seed (%s): %v", sql, err)
		}
	}
	q(&bA1, "INSERT INTO branches (tenant_id,name) VALUES ($1,'A-Centro') RETURNING id::text", a)
	q(&bA2, "INSERT INTO branches (tenant_id,name) VALUES ($1,'A-Norte') RETURNING id::text", a)
	q(&bB1, "INSERT INTO branches (tenant_id,name) VALUES ($1,'B-Centro') RETURNING id::text", b)
	q(&userA, "INSERT INTO users (tenant_id,email,password_hash,name,role) VALUES ($1,'ra@t.test','x','Ana','cashier') RETURNING id::text", a)
	q(&userB, "INSERT INTO users (tenant_id,email,password_hash,name,role) VALUES ($1,'rb@t.test','x','Beto','cashier') RETURNING id::text", b)
	q(&catServ, "INSERT INTO expense_categories (tenant_id,name) VALUES ($1,'Servicios') RETURNING id::text", a)
	q(&conLuz, "INSERT INTO expense_concepts (tenant_id,category_id,name) VALUES ($1,$2,'Luz') RETURNING id::text", a, catServ)
	q(&conVarios, "INSERT INTO expense_concepts (tenant_id,name) VALUES ($1,'Varios') RETURNING id::text", a) // sin categoría
	q(&conB, "INSERT INTO expense_concepts (tenant_id,name) VALUES ($1,'AguaB') RETURNING id::text", b)

	ins := func(tenant, branch, concept, cname string, amount int, by string) {
		if _, err := pool.Exec(ctx,
			"INSERT INTO expenses (tenant_id,branch_id,concept_id,concept_name,amount_cents,created_by) VALUES ($1,$2,$3,$4,$5,$6)",
			tenant, branch, concept, cname, amount, by); err != nil {
			t.Fatalf("seed gasto: %v", err)
		}
	}
	// Tenant A: Servicios/Luz 100 (A1) + 200 (A2); Sin categoría/Varios 50 (A1).
	ins(a, bA1, conLuz, "Luz", 100, userA)
	ins(a, bA2, conLuz, "Luz", 200, userA)
	ins(a, bA1, conVarios, "Varios", 50, userA)
	// Tenant B: 999 (aislamiento).
	ins(b, bB1, conB, "AguaB", 999, userB)

	from := time.Now().Add(-time.Hour)
	to := time.Now().Add(time.Hour)

	// Sin filtro de sucursal: totales del tenant A.
	rep, err := svc.ExpensesReport(ctx, a, from, to, BranchFilter{})
	if err != nil {
		t.Fatalf("reporte gastos: %v", err)
	}
	if rep.Summary.ExpensesCount != 3 || rep.Summary.TotalCents != 350 {
		t.Fatalf("resumen: esperaba count=3 total=350, obtuvo %+v", rep.Summary)
	}

	// Por categoría: "Servicios" (300) y "Sin categoría" (50).
	byCat := map[string]int{}
	for _, c := range rep.ByCategory {
		byCat[c.CategoryName] = c.TotalCents
	}
	if byCat["Servicios"] != 300 || byCat["Sin categoría"] != 50 {
		t.Fatalf("por categoría inesperado: %+v", rep.ByCategory)
	}

	// Por sucursal: A1 (150) y A2 (200).
	byBr := map[string]int{}
	for _, br := range rep.ByBranch {
		if br.BranchID != nil {
			byBr[*br.BranchID] = br.TotalCents
		}
	}
	if byBr[bA1] != 150 || byBr[bA2] != 200 {
		t.Fatalf("por sucursal inesperado: %+v", rep.ByBranch)
	}

	// BranchFilter a A1: solo 150 (2 gastos).
	repA1, _ := svc.ExpensesReport(ctx, a, from, to, BranchFilter{ID: &bA1})
	if repA1.Summary.ExpensesCount != 2 || repA1.Summary.TotalCents != 150 {
		t.Fatalf("filtro A1: esperaba count=2 total=150, obtuvo %+v", repA1.Summary)
	}

	// Aislamiento: tenant B solo ve su gasto.
	repB, _ := svc.ExpensesReport(ctx, b, from, to, BranchFilter{})
	if repB.Summary.ExpensesCount != 1 || repB.Summary.TotalCents != 999 {
		t.Fatalf("aislamiento B: %+v", repB.Summary)
	}
}
