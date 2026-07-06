package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// testService levanta el servicio contra una DB de test. Se omite si no hay
// TEST_DATABASE_URL (los unit tests no necesitan DB). La DB debe estar migrada.
func testService(t *testing.T) (*Service, *pgxpool.Pool) {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL no definido; se omiten tests de integración")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Skipf("no se pudo abrir el pool de test: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Skipf("DB de test no disponible: %v", err)
	}
	if _, err := pool.Exec(ctx, "TRUNCATE users, tenants RESTART IDENTITY CASCADE"); err != nil {
		t.Fatalf("limpiando tablas: %v", err)
	}
	return NewService(pool, "test-secret", time.Hour, false), pool
}

func postJSON(t *testing.T, c *http.Client, url string, body any) *http.Response {
	t.Helper()
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	return resp
}

func TestLoginFlow(t *testing.T) {
	svc, pool := testService(t)
	defer pool.Close()
	ctx := context.Background()

	// T2: seed idempotente.
	created, err := svc.SeedSuperAdmin(ctx, "admin@faro.test", "secret123")
	if err != nil || !created {
		t.Fatalf("seed: created=%v err=%v", created, err)
	}
	again, err := svc.SeedSuperAdmin(ctx, "admin@faro.test", "secret123")
	if err != nil || again {
		t.Fatalf("el seed debería ser idempotente: again=%v err=%v", again, err)
	}

	srv := httptest.NewServer(svc.Routes())
	defer srv.Close()
	client := srv.Client()

	// Login OK -> 200 + cookie de sesión.
	resp := postJSON(t, client, srv.URL+"/login", map[string]string{"email": "admin@faro.test", "password": "secret123"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login: esperaba 200, obtuvo %d", resp.StatusCode)
	}
	var session *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == sessionCookieName {
			session = c
		}
	}
	if session == nil {
		t.Fatal("no se recibió la cookie de sesión")
	}

	// /me con sesión -> 200.
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/me", nil)
	req.AddCookie(session)
	me, err := client.Do(req)
	if err != nil {
		t.Fatalf("me: %v", err)
	}
	if me.StatusCode != http.StatusOK {
		t.Fatalf("/me: esperaba 200, obtuvo %d", me.StatusCode)
	}

	// /me sin sesión -> 401.
	noauth, _ := client.Get(srv.URL + "/me")
	if noauth.StatusCode != http.StatusUnauthorized {
		t.Fatalf("/me sin sesión: esperaba 401, obtuvo %d", noauth.StatusCode)
	}
}

// sessionCookieFrom extrae la cookie de sesión de una respuesta.
func sessionCookieFrom(resp *http.Response) *http.Cookie {
	for _, c := range resp.Cookies() {
		if c.Name == sessionCookieName {
			return c
		}
	}
	return nil
}

// TestSessionTTLByRole verifica que la expiración de la cookie de sesión (y por ende
// del JWT) depende del rol: 7 días para super_admin/branch_admin, 20h para cashier.
func TestSessionTTLByRole(t *testing.T) {
	svc, pool := testService(t)
	defer pool.Close()
	ctx := context.Background()

	if _, err := svc.SeedSuperAdmin(ctx, "root@faro.test", "secret123"); err != nil {
		t.Fatalf("seed super admin: %v", err)
	}
	tenant, _, err := svc.CreateTenantWithOwner(ctx, CreateTenantInput{
		Name: "Vanta", OwnerEmail: "owner@vanta.test", OwnerPassword: "secret123", OwnerName: "Owner",
	})
	if err != nil {
		t.Fatalf("crear negocio: %v", err)
	}
	var branchID string
	if err := pool.QueryRow(ctx, "INSERT INTO branches (tenant_id, name) VALUES ($1,'Centro') RETURNING id::text", tenant.ID).Scan(&branchID); err != nil {
		t.Fatalf("crear sucursal: %v", err)
	}
	if _, err := svc.CreateUser(ctx, tenant.ID, "cajero@vanta.test", "secret123", "Cajero", RoleCashier, []string{branchID}); err != nil {
		t.Fatalf("crear cajero: %v", err)
	}

	srv := httptest.NewServer(svc.Routes())
	defer srv.Close()
	client := srv.Client()

	assertTTL := func(email, pass string, want time.Duration) {
		t.Helper()
		before := time.Now()
		resp := postJSON(t, client, srv.URL+"/login", map[string]string{"email": email, "password": pass})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("login %s: esperaba 200, obtuvo %d", email, resp.StatusCode)
		}
		c := sessionCookieFrom(resp)
		if c == nil {
			t.Fatalf("login %s: sin cookie de sesión", email)
		}
		got := c.Expires.Sub(before)
		if d := got - want; d < -2*time.Minute || d > 2*time.Minute {
			t.Fatalf("TTL de %s = %v, esperaba ≈ %v", email, got, want)
		}
	}

	assertTTL("root@faro.test", "secret123", 7*24*time.Hour)  // super_admin
	assertTTL("cajero@vanta.test", "secret123", 20*time.Hour) // cashier
}

func TestLoginInvalidCredentials(t *testing.T) {
	svc, pool := testService(t)
	defer pool.Close()
	if _, err := svc.SeedSuperAdmin(context.Background(), "admin@faro.test", "secret123"); err != nil {
		t.Fatalf("seed: %v", err)
	}

	srv := httptest.NewServer(svc.Routes())
	defer srv.Close()

	resp := postJSON(t, srv.Client(), srv.URL+"/login", map[string]string{"email": "admin@faro.test", "password": "incorrecta"})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("login inválido: esperaba 401, obtuvo %d", resp.StatusCode)
	}
}
