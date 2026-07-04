package branches

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// testSvc devuelve el service y dos negocios sembrados (A y B).
func testSvc(t *testing.T) (svc *Service, pool *pgxpool.Pool, a, b string) {
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
		"TRUNCATE branches, sale_items, sales, customers, products, categories, users, tenants RESTART IDENTITY CASCADE"); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	pool.QueryRow(ctx, "INSERT INTO tenants (name) VALUES ('A') RETURNING id::text").Scan(&a)
	pool.QueryRow(ctx, "INSERT INTO tenants (name) VALUES ('B') RETURNING id::text").Scan(&b)
	return NewService(pool), pool, a, b
}

func TestBranchCRUD(t *testing.T) {
	svc, pool, a, _ := testSvc(t)
	defer pool.Close()
	ctx := context.Background()

	if items, err := svc.List(ctx, a, "active"); err != nil || len(items) != 0 {
		t.Fatalf("lista inicial: err=%v n=%d", err, len(items))
	}

	created, err := svc.Create(ctx, a, "Vanta Centro")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.Status != "active" || created.Name != "Vanta Centro" || created.TenantID != a {
		t.Fatalf("sucursal creada inesperada: %+v", created)
	}

	got, err := svc.Get(ctx, a, created.ID)
	if err != nil || got.Name != "Vanta Centro" {
		t.Fatalf("get: err=%v got=%+v", err, got)
	}

	// Renombrar + desactivar.
	newName := "Vanta Norte"
	inactive := "inactive"
	updated, err := svc.Update(ctx, a, created.ID, BranchPatch{Name: &newName, Status: &inactive})
	if err != nil || updated.Name != "Vanta Norte" || updated.Status != "inactive" {
		t.Fatalf("update: err=%v got=%+v", err, updated)
	}

	// Ya no aparece en active; sí en la lista completa.
	if items, _ := svc.List(ctx, a, "active"); len(items) != 0 {
		t.Fatalf("inactiva no debía aparecer en active, n=%d", len(items))
	}
	if items, _ := svc.List(ctx, a, ""); len(items) != 1 {
		t.Fatalf("lista completa esperaba 1, n=%d", len(items))
	}

	// Borrado físico (sin referencias) => ok.
	if err := svc.Delete(ctx, a, created.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := svc.Get(ctx, a, created.ID); err != ErrNotFound {
		t.Fatalf("get tras delete: esperaba ErrNotFound, obtuvo %v", err)
	}
}

func TestBranchNameUniqueness(t *testing.T) {
	svc, pool, a, b := testSvc(t)
	defer pool.Close()
	ctx := context.Background()

	if _, err := svc.Create(ctx, a, "Centro"); err != nil {
		t.Fatalf("create 1: %v", err)
	}
	// Mismo nombre en el mismo negocio => 409.
	if _, err := svc.Create(ctx, a, "Centro"); err != ErrNameTaken {
		t.Fatalf("nombre duplicado: esperaba ErrNameTaken, obtuvo %v", err)
	}
	// Mismo nombre en OTRO negocio => permitido.
	if _, err := svc.Create(ctx, b, "Centro"); err != nil {
		t.Fatalf("mismo nombre en otro negocio debía permitirse: %v", err)
	}

	// Rename colisionando con una existente => 409.
	other, _ := svc.Create(ctx, a, "Norte")
	name := "Centro"
	if _, err := svc.Update(ctx, a, other.ID, BranchPatch{Name: &name}); err != ErrNameTaken {
		t.Fatalf("rename colisionando: esperaba ErrNameTaken, obtuvo %v", err)
	}
}

func TestBranchValidation(t *testing.T) {
	svc, pool, a, _ := testSvc(t)
	defer pool.Close()
	ctx := context.Background()

	if _, err := svc.Create(ctx, a, "   "); err != ErrValidation {
		t.Fatalf("nombre vacío: esperaba ErrValidation, obtuvo %v", err)
	}
	long := make([]byte, 61)
	for i := range long {
		long[i] = 'x'
	}
	if _, err := svc.Create(ctx, a, string(long)); err != ErrValidation {
		t.Fatalf("nombre largo: esperaba ErrValidation, obtuvo %v", err)
	}

	b, _ := svc.Create(ctx, a, "OK")
	bad := "archived"
	if _, err := svc.Update(ctx, a, b.ID, BranchPatch{Status: &bad}); err != ErrValidation {
		t.Fatalf("status inválido: esperaba ErrValidation, obtuvo %v", err)
	}
}

func TestBranchTenantIsolation(t *testing.T) {
	svc, pool, a, b := testSvc(t)
	defer pool.Close()
	ctx := context.Background()

	created, _ := svc.Create(ctx, a, "Solo A")
	if _, err := svc.Get(ctx, b, created.ID); err != ErrNotFound {
		t.Fatalf("get cross-tenant: esperaba ErrNotFound, obtuvo %v", err)
	}
	name := "Hack"
	if _, err := svc.Update(ctx, b, created.ID, BranchPatch{Name: &name}); err != ErrNotFound {
		t.Fatalf("update cross-tenant: esperaba ErrNotFound, obtuvo %v", err)
	}
	if err := svc.Delete(ctx, b, created.ID); err != ErrNotFound {
		t.Fatalf("delete cross-tenant: esperaba ErrNotFound, obtuvo %v", err)
	}
}

func TestBranchDeleteInUse(t *testing.T) {
	svc, pool, a, _ := testSvc(t)
	defer pool.Close()
	ctx := context.Background()

	// Sucursal referenciada por una membresía de usuario => 409 branch_in_use.
	byUser, _ := svc.Create(ctx, a, "Con usuario")
	var uid string
	pool.QueryRow(ctx,
		`INSERT INTO users (tenant_id, email, password_hash, name) VALUES ($1,'u@a.test','h','U') RETURNING id::text`, a).Scan(&uid)
	pool.Exec(ctx,
		`INSERT INTO user_branches (user_id, branch_id, tenant_id) VALUES ($1,$2,$3)`, uid, byUser.ID, a)
	if err := svc.Delete(ctx, a, byUser.ID); err != ErrInUse {
		t.Fatalf("delete con usuario: esperaba ErrInUse, obtuvo %v", err)
	}

	// Sucursal referenciada por una venta => 409 branch_in_use.
	bySale, _ := svc.Create(ctx, a, "Con venta")
	pool.Exec(ctx,
		`INSERT INTO sales (tenant_id, total_cents, amount_paid_cents, change_cents, payment_method, branch_id)
		 VALUES ($1,1000,1000,0,'cash',$2)`, a, bySale.ID)
	if err := svc.Delete(ctx, a, bySale.ID); err != ErrInUse {
		t.Fatalf("delete con venta: esperaba ErrInUse, obtuvo %v", err)
	}

	// Tras liberar la referencia, se puede borrar.
	pool.Exec(ctx, `DELETE FROM sales WHERE branch_id = $1`, bySale.ID)
	if err := svc.Delete(ctx, a, bySale.ID); err != nil {
		t.Fatalf("delete tras liberar: %v", err)
	}
}
