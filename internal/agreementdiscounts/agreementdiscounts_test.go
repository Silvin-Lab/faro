package agreementdiscounts

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// testSvc devuelve el service y dos negocios sembrados (A y B) para aislar tenants.
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
	if _, err := pool.Exec(ctx,
		"TRUNCATE agreement_discounts, sale_items, sales, tenants RESTART IDENTITY CASCADE"); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	var a, b string
	pool.QueryRow(ctx, "INSERT INTO tenants (name) VALUES ('A') RETURNING id::text").Scan(&a)
	pool.QueryRow(ctx, "INSERT INTO tenants (name) VALUES ('B') RETURNING id::text").Scan(&b)
	return NewService(pool), pool, a, b
}

func TestAgreementDiscountCRUD(t *testing.T) {
	svc, pool, a, _ := testSvc(t)
	defer pool.Close()
	ctx := context.Background()

	if items, err := svc.List(ctx, a, "active"); err != nil || len(items) != 0 {
		t.Fatalf("lista inicial: err=%v n=%d", err, len(items))
	}

	d, err := svc.Create(ctx, a, 10)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if d.Percent != 10 || d.Status != "active" {
		t.Fatalf("creado inesperado: %+v", d)
	}

	// Segundo % distinto; lista activa ordenada ASC por %.
	if _, err := svc.Create(ctx, a, 15); err != nil {
		t.Fatalf("create 15: %v", err)
	}
	items, err := svc.List(ctx, a, "active")
	if err != nil || len(items) != 2 || items[0].Percent != 10 || items[1].Percent != 15 {
		t.Fatalf("lista activa: err=%v %+v", err, items)
	}

	// Update del %.
	up, err := svc.Update(ctx, a, d.ID, 12)
	if err != nil || up.Percent != 12 {
		t.Fatalf("update: err=%v %+v", err, up)
	}

	// Get.
	got, err := svc.Get(ctx, a, d.ID)
	if err != nil || got.Percent != 12 {
		t.Fatalf("get: err=%v %+v", err, got)
	}
}

func TestAgreementDiscountValidation(t *testing.T) {
	svc, pool, a, _ := testSvc(t)
	defer pool.Close()
	ctx := context.Background()

	for _, p := range []int{0, -5, 101, 200} {
		if _, err := svc.Create(ctx, a, p); !errors.Is(err, ErrValidation) {
			t.Fatalf("percent %d: esperaba ErrValidation, obtuvo %v", p, err)
		}
	}
}

func TestAgreementDiscountDuplicateActive(t *testing.T) {
	svc, pool, a, _ := testSvc(t)
	defer pool.Close()
	ctx := context.Background()

	if _, err := svc.Create(ctx, a, 10); err != nil {
		t.Fatalf("create: %v", err)
	}
	// Segundo activo con el mismo % => choca con el índice parcial.
	if _, err := svc.Create(ctx, a, 10); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("duplicado activo: esperaba ErrDuplicate, obtuvo %v", err)
	}
}

// TestAgreementDiscountArchiveAndRecreate: archivar y recrear el mismo % es válido
// (el índice único es parcial sobre los activos).
func TestAgreementDiscountArchiveAndRecreate(t *testing.T) {
	svc, pool, a, _ := testSvc(t)
	defer pool.Close()
	ctx := context.Background()

	d, err := svc.Create(ctx, a, 10)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := svc.Archive(ctx, a, d.ID); err != nil {
		t.Fatalf("archive: %v", err)
	}
	// Ya no aparece entre los activos; sí entre los archivados.
	if items, _ := svc.List(ctx, a, "active"); len(items) != 0 {
		t.Fatalf("activos tras archivar: %d (esperaba 0)", len(items))
	}
	if items, _ := svc.List(ctx, a, "inactive"); len(items) != 1 {
		t.Fatalf("archivados: %d (esperaba 1)", len(items))
	}
	// Recrear el mismo % ahora es válido.
	if _, err := svc.Create(ctx, a, 10); err != nil {
		t.Fatalf("recrear 10 tras archivar: %v", err)
	}
}

func TestAgreementDiscountArchiveNonexistent(t *testing.T) {
	svc, pool, a, _ := testSvc(t)
	defer pool.Close()
	ctx := context.Background()
	if err := svc.Archive(ctx, a, "00000000-0000-0000-0000-000000000000"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("archivar inexistente: esperaba ErrNotFound, obtuvo %v", err)
	}
}

// TestAgreementDiscountTenantIsolation: un negocio no ve ni toca los descuentos del
// otro.
func TestAgreementDiscountTenantIsolation(t *testing.T) {
	svc, pool, a, b := testSvc(t)
	defer pool.Close()
	ctx := context.Background()

	dA, err := svc.Create(ctx, a, 10)
	if err != nil {
		t.Fatalf("create A: %v", err)
	}
	// B no lo ve en su lista ni lo obtiene por id.
	if items, _ := svc.List(ctx, b, "all"); len(items) != 0 {
		t.Fatalf("B ve descuentos de A: %d", len(items))
	}
	if _, err := svc.Get(ctx, b, dA.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("B get descuento de A: esperaba ErrNotFound, obtuvo %v", err)
	}
	// Mismo % activo en B es válido (unicidad es por tenant).
	if _, err := svc.Create(ctx, b, 10); err != nil {
		t.Fatalf("B crea 10: %v", err)
	}
}
