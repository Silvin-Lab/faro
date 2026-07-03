package loyalty

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// testSvc devuelve el service y datos sembrados: negocios A y B, dos productos de
// A (p1, p2) y un producto ajeno de B (pB).
func testSvc(t *testing.T) (svc *Service, pool *pgxpool.Pool, a, b, p1, p2, pB string) {
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
		"TRUNCATE loyalty_redemptions, loyalty_promotion_products, loyalty_promotions, sale_items, sales, customers, products, categories, users, tenants RESTART IDENTITY CASCADE"); err != nil {
		t.Fatalf("truncate: %v", err)
	}

	pool.QueryRow(ctx, "INSERT INTO tenants (name) VALUES ('A') RETURNING id::text").Scan(&a)
	pool.QueryRow(ctx, "INSERT INTO tenants (name) VALUES ('B') RETURNING id::text").Scan(&b)
	pool.QueryRow(ctx, "INSERT INTO products (tenant_id, name, price_cents) VALUES ($1,'Latte',5000) RETURNING id::text", a).Scan(&p1)
	pool.QueryRow(ctx, "INSERT INTO products (tenant_id, name, price_cents) VALUES ($1,'Muffin',3000) RETURNING id::text", a).Scan(&p2)
	pool.QueryRow(ctx, "INSERT INTO products (tenant_id, name, price_cents) VALUES ($1,'Ajeno',9999) RETURNING id::text", b).Scan(&pB)
	return NewService(pool), pool, a, b, p1, p2, pB
}

func TestPromotionCRUD(t *testing.T) {
	svc, pool, a, _, p1, p2, _ := testSvc(t)
	defer pool.Close()
	ctx := context.Background()

	// Lista vacía al inicio.
	if items, err := svc.List(ctx, a, "active"); err != nil || len(items) != 0 {
		t.Fatalf("lista inicial: err=%v n=%d", err, len(items))
	}

	created, err := svc.Create(ctx, a, PromotionInput{
		Name: "50% café", DiscountPercent: 50, VisitThreshold: 3,
		ResetsCounter: false, ProductIDs: []string{p1, p2},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.Status != "active" || len(created.ProductIDs) != 2 {
		t.Fatalf("promo creada inesperada: %+v", created)
	}

	got, err := svc.Get(ctx, a, created.ID)
	if err != nil || got.Name != "50% café" || got.DiscountPercent != 50 || got.VisitThreshold != 3 {
		t.Fatalf("get: err=%v got=%+v", err, got)
	}

	updated, err := svc.Update(ctx, a, created.ID, PromotionInput{
		Name: "Gratis", DiscountPercent: 100, VisitThreshold: 5,
		ResetsCounter: true, ProductIDs: []string{p1},
	})
	if err != nil || updated.DiscountPercent != 100 || !updated.ResetsCounter || len(updated.ProductIDs) != 1 {
		t.Fatalf("update: err=%v got=%+v", err, updated)
	}

	// Archivar => status inactive; ya no aparece en 'active' pero sí en 'all'.
	if err := svc.Archive(ctx, a, created.ID); err != nil {
		t.Fatalf("archive: %v", err)
	}
	if items, _ := svc.List(ctx, a, "active"); len(items) != 0 {
		t.Fatalf("archivada no debía aparecer en active, n=%d", len(items))
	}
	if items, _ := svc.List(ctx, a, "all"); len(items) != 1 || items[0].Status != "inactive" {
		t.Fatalf("all debía traer 1 inactive, got=%+v", items)
	}
}

func TestPromotionValidation(t *testing.T) {
	svc, pool, a, _, p1, _, pB := testSvc(t)
	defer pool.Close()
	ctx := context.Background()

	cases := []PromotionInput{
		{Name: "", DiscountPercent: 50, VisitThreshold: 3, ProductIDs: []string{p1}},        // nombre vacío
		{Name: "x", DiscountPercent: 0, VisitThreshold: 3, ProductIDs: []string{p1}},         // % < 1
		{Name: "x", DiscountPercent: 101, VisitThreshold: 3, ProductIDs: []string{p1}},       // % > 100
		{Name: "x", DiscountPercent: 50, VisitThreshold: 0, ProductIDs: []string{p1}},        // umbral <= 0
		{Name: "x", DiscountPercent: 50, VisitThreshold: 3, ProductIDs: []string{}},          // sin productos
		{Name: "x", DiscountPercent: 50, VisitThreshold: 3, ProductIDs: []string{pB}},        // producto ajeno
	}
	for i, in := range cases {
		if _, err := svc.Create(ctx, a, in); err != ErrValidation {
			t.Fatalf("caso %d: esperaba ErrValidation, obtuvo %v", i, err)
		}
	}
}

func TestPromotionTenantIsolation(t *testing.T) {
	svc, pool, a, b, p1, _, _ := testSvc(t)
	defer pool.Close()
	ctx := context.Background()

	created, err := svc.Create(ctx, a, PromotionInput{
		Name: "A", DiscountPercent: 10, VisitThreshold: 2, ProductIDs: []string{p1},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// B no ve ni puede tocar la promoción de A.
	if _, err := svc.Get(ctx, b, created.ID); err != ErrNotFound {
		t.Fatalf("get cross-tenant: esperaba ErrNotFound, obtuvo %v", err)
	}
	if err := svc.Archive(ctx, b, created.ID); err != ErrNotFound {
		t.Fatalf("archive cross-tenant: esperaba ErrNotFound, obtuvo %v", err)
	}
}

func TestCustomerStatusEligibility(t *testing.T) {
	svc, pool, a, _, p1, p2, _ := testSvc(t)
	defer pool.Close()
	ctx := context.Background()

	// Cliente con visits=2, visits_lifetime=9.
	var cust string
	pool.QueryRow(ctx,
		"INSERT INTO customers (tenant_id, phone, first_name, last_name, visits, visits_lifetime) VALUES ($1,'555','Ana','Paz',2,9) RETURNING id::text", a).Scan(&cust)

	// Tres promociones activas con umbrales 3, 5 y 7.
	if _, err := svc.Create(ctx, a, PromotionInput{Name: "A", DiscountPercent: 50, VisitThreshold: 3, ProductIDs: []string{p1}}); err != nil {
		t.Fatalf("promo A: %v", err)
	}
	if _, err := svc.Create(ctx, a, PromotionInput{Name: "B", DiscountPercent: 50, VisitThreshold: 5, ProductIDs: []string{p2}}); err != nil {
		t.Fatalf("promo B: %v", err)
	}
	if _, err := svc.Create(ctx, a, PromotionInput{Name: "C", DiscountPercent: 100, VisitThreshold: 7, ProductIDs: []string{p1}}); err != nil {
		t.Fatalf("promo C: %v", err)
	}

	st, err := svc.CustomerStatus(ctx, a, cust)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if st.Visits != 2 || st.VisitsLifetime != 9 || len(st.Promotions) != 3 {
		t.Fatalf("status base inesperado: %+v", st)
	}
	// Orden ascendente por visitsRemaining: A(1) primero, luego B(3), C(5).
	wantRemaining := []int{1, 3, 5}
	wantApplicable := []bool{true, false, false}
	wantName := []string{"A", "B", "C"}
	for i, ps := range st.Promotions {
		if ps.Name != wantName[i] {
			t.Fatalf("promo[%d] nombre esperaba %s, obtuvo %s", i, wantName[i], ps.Name)
		}
		if ps.VisitsRemaining != wantRemaining[i] {
			t.Fatalf("promo[%d]=%s visitsRemaining esperaba %d, obtuvo %d", i, ps.Name, wantRemaining[i], ps.VisitsRemaining)
		}
		if ps.ApplicableNow != wantApplicable[i] {
			t.Fatalf("promo[%d]=%s applicableNow esperaba %v, obtuvo %v", i, ps.Name, wantApplicable[i], ps.ApplicableNow)
		}
		if len(ps.Products) != 1 {
			t.Fatalf("promo[%d]=%s esperaba 1 producto, obtuvo %d", i, ps.Name, len(ps.Products))
		}
	}

	// Cliente inexistente / de otro negocio => not found.
	if _, err := svc.CustomerStatus(ctx, a, "00000000-0000-0000-0000-000000000000"); err != ErrNotFound {
		t.Fatalf("status cliente inexistente: esperaba ErrNotFound, obtuvo %v", err)
	}
}
