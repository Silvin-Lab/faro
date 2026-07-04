package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
)

// fullRouter monta auth + provisión como en el servidor real (M7 v2: sin /tenants).
func fullRouter(svc *Service) http.Handler {
	r := chi.NewRouter()
	r.Mount("/auth", svc.Routes())
	r.Mount("/users", svc.UserRoutes())
	return r
}

func jarClient(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar: %v", err)
	}
	return &http.Client{Jar: jar}
}

func post(t *testing.T, c *http.Client, url string, body any) *http.Response {
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

func login(t *testing.T, c *http.Client, base, email, pass string) {
	t.Helper()
	if resp := post(t, c, base+"/auth/login", map[string]string{"email": email, "password": pass}); resp.StatusCode != http.StatusOK {
		t.Fatalf("login %s: esperaba 200, obtuvo %d", email, resp.StatusCode)
	}
}

func usersCount(t *testing.T, c *http.Client, base string) int {
	t.Helper()
	resp, err := c.Get(base + "/users")
	if err != nil {
		t.Fatalf("GET /users: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /users: esperaba 200, obtuvo %d", resp.StatusCode)
	}
	var body struct {
		Items []map[string]any `json:"items"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&body)
	return len(body.Items)
}

func TestLogoutInvalidatesSession(t *testing.T) {
	svc, pool := testService(t)
	defer pool.Close()
	if _, err := svc.SeedSuperAdmin(context.Background(), "admin@faro.test", "secret123"); err != nil {
		t.Fatalf("seed: %v", err)
	}

	srv := httptest.NewServer(fullRouter(svc))
	defer srv.Close()
	c := jarClient(t)

	login(t, c, srv.URL, "admin@faro.test", "secret123")

	// Con sesión: /me = 200.
	if resp, err := c.Get(srv.URL + "/auth/me"); err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("/me con sesión: esperaba 200, obtuvo %v", resp.StatusCode)
	}

	// Logout = 204 y limpia la cookie.
	if resp := post(t, c, srv.URL+"/auth/logout", map[string]string{}); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("logout: esperaba 204, obtuvo %d", resp.StatusCode)
	}

	// Tras logout: /me = 401.
	if resp, err := c.Get(srv.URL + "/auth/me"); err != nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("/me tras logout: esperaba 401, obtuvo %v", resp.StatusCode)
	}
}

// TestUserProvisioningM7 cubre la gestión de usuarios v2: solo super admin, membresía
// M:N (branchIds), validación de sucursales del negocio y aislamiento de rol.
func TestUserProvisioningM7(t *testing.T) {
	svc, pool := testService(t)
	defer pool.Close()
	ctx := context.Background()

	if _, err := svc.SeedSuperAdmin(ctx, "root@faro.test", "secret123"); err != nil {
		t.Fatalf("seed super admin: %v", err)
	}
	// Negocio único + una sucursal.
	tenant, _, err := svc.CreateTenantWithOwner(ctx, CreateTenantInput{
		Name: "Vanta", OwnerEmail: "owner@vanta.test", OwnerPassword: "secret123", OwnerName: "Owner",
	})
	if err != nil {
		t.Fatalf("crear negocio: %v", err)
	}
	var branchID string
	pool.QueryRow(ctx, "INSERT INTO branches (tenant_id, name) VALUES ($1,'Centro') RETURNING id::text", tenant.ID).Scan(&branchID)

	srv := httptest.NewServer(fullRouter(svc))
	defer srv.Close()

	root := jarClient(t)
	login(t, root, srv.URL, "root@faro.test", "secret123")

	// Alta de un cajero con sucursal (M:N).
	resp := post(t, root, srv.URL+"/users", map[string]any{
		"email": "cajero@vanta.test", "password": "secret123", "name": "Cajero", "branchIds": []string{branchID},
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("crear cajero: esperaba 201, obtuvo %d", resp.StatusCode)
	}
	var created struct {
		User User `json:"user"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&created)
	if len(created.User.Branches) != 1 || created.User.Branches[0].ID != branchID {
		t.Fatalf("cajero sin membresía esperada: %+v", created.User.Branches)
	}

	// branchIds vacío => 400 validation_error.
	if resp := post(t, root, srv.URL+"/users", map[string]any{
		"email": "x@vanta.test", "password": "secret123", "name": "X", "branchIds": []string{},
	}); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("branchIds vacío: esperaba 400, obtuvo %d", resp.StatusCode)
	}

	// Sucursal inexistente => 404 branch_not_found.
	if resp := post(t, root, srv.URL+"/users", map[string]any{
		"email": "y@vanta.test", "password": "secret123", "name": "Y",
		"branchIds": []string{"00000000-0000-0000-0000-000000000000"},
	}); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("sucursal inexistente: esperaba 404, obtuvo %d", resp.StatusCode)
	}

	// Email duplicado => 409.
	if resp := post(t, root, srv.URL+"/users", map[string]any{
		"email": "cajero@vanta.test", "password": "secret123", "name": "Dup", "branchIds": []string{branchID},
	}); resp.StatusCode != http.StatusConflict {
		t.Fatalf("email duplicado: esperaba 409, obtuvo %d", resp.StatusCode)
	}

	// GET /users lista al owner + cajero (2), cada uno con branches.
	if n := usersCount(t, root, srv.URL); n != 2 {
		t.Fatalf("GET /users esperaba 2 usuarios, obtuvo %d", n)
	}

	// Un usuario de sucursal NO administra usuarios => 403 forbidden.
	staff := jarClient(t)
	login(t, staff, srv.URL, "cajero@vanta.test", "secret123")
	if resp := post(t, staff, srv.URL+"/users", map[string]any{
		"email": "z@vanta.test", "password": "secret123", "name": "Z", "branchIds": []string{branchID},
	}); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cajero creando usuario: esperaba 403, obtuvo %d", resp.StatusCode)
	}
}
