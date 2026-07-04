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
	if _, err := pool.Exec(ctx, "TRUNCATE sale_items, sales, products, categories, customers, users, tenants RESTART IDENTITY CASCADE"); err != nil {
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
