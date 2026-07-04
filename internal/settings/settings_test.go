package settings

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func testSvc(t *testing.T) (*Service, *pgxpool.Pool, string) {
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
	if _, err := pool.Exec(ctx, "TRUNCATE branches, users, tenants RESTART IDENTITY CASCADE"); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	var a string
	pool.QueryRow(ctx, "INSERT INTO tenants (name) VALUES ('A') RETURNING id::text").Scan(&a)
	return NewService(pool), pool, a
}

func TestSettingsFavicon(t *testing.T) {
	svc, pool, a := testSvc(t)
	defer pool.Close()
	ctx := context.Background()

	// Sin favicon al inicio.
	st, err := svc.Get(ctx, a)
	if err != nil || st.FaviconURL != nil || st.Tenant.ID != a || st.Tenant.Name != "A" {
		t.Fatalf("settings inicial: err=%v st=%+v", err, st)
	}

	// Persistir un favicon válido (/files/*).
	url, err := svc.SetFavicon(ctx, a, "/files/ab12.webp")
	if err != nil || url != "/files/ab12.webp" {
		t.Fatalf("set favicon: err=%v url=%q", err, url)
	}
	st, _ = svc.Get(ctx, a)
	if st.FaviconURL == nil || *st.FaviconURL != "/files/ab12.webp" {
		t.Fatalf("favicon persistido inesperado: %+v", st.FaviconURL)
	}

	// URLs inválidas (host arbitrario o vacío) => ErrValidation.
	for _, bad := range []string{"", "http://evil.test/x.png", "/uploads/x.png", "files/x.webp"} {
		if _, err := svc.SetFavicon(ctx, a, bad); err != ErrValidation {
			t.Fatalf("favicon inválido %q: esperaba ErrValidation, obtuvo %v", bad, err)
		}
	}

	// Quitar el favicon => NULL.
	if err := svc.ClearFavicon(ctx, a); err != nil {
		t.Fatalf("clear favicon: %v", err)
	}
	if st, _ := svc.Get(ctx, a); st.FaviconURL != nil {
		t.Fatalf("favicon debía quedar en NULL, obtuvo %+v", st.FaviconURL)
	}
}
