package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"faro/internal/auth"
	"faro/internal/branches"
	"faro/internal/categories"
	"faro/internal/customers"
	"faro/internal/loyalty"
	"faro/internal/products"
	"faro/internal/reports"
	"faro/internal/sales"
	"faro/internal/server"
	"faro/internal/settings"
	"faro/internal/uploads"
)

type m7Env struct {
	srv      *httptest.Server
	pool     *pgxpool.Pool
	tenantID string
	productA string
}

func setupM7(t *testing.T) *m7Env {
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
		"TRUNCATE user_branches, loyalty_redemptions, loyalty_promotion_products, loyalty_promotions, sale_items, sales, customers, products, categories, branches, users, tenants RESTART IDENTITY CASCADE"); err != nil {
		t.Fatalf("truncate: %v", err)
	}

	authSvc := auth.NewService(pool, "test-secret", time.Hour, false)
	if _, err := authSvc.SeedSuperAdmin(ctx, "root@faro.test", "secret123"); err != nil {
		t.Fatalf("seed super admin: %v", err)
	}
	dir := t.TempDir()
	uploadsH := uploads.New(dir, authSvc.RequireSuperAdmin)
	handler := server.New(pool, "http://localhost:3000", authSvc,
		categories.NewService(pool), products.NewService(pool), sales.NewService(pool),
		customers.NewService(pool), reports.NewService(pool), loyalty.NewService(pool),
		branches.NewService(pool), settings.NewService(pool), uploadsH, dir)

	env := &m7Env{srv: httptest.NewServer(handler), pool: pool}

	// Negocio único (una fila en tenants) + un producto activo.
	tenant, _, err := authSvc.CreateTenantWithOwner(ctx, auth.CreateTenantInput{
		Name: "Vanta", OwnerEmail: "owner@vanta.test", OwnerPassword: "secret123", OwnerName: "Owner",
	})
	if err != nil {
		t.Fatalf("crear negocio: %v", err)
	}
	env.tenantID = tenant.ID
	pool.QueryRow(ctx, "INSERT INTO products (tenant_id, name, price_cents) VALUES ($1,'Latte',5000) RETURNING id::text", env.tenantID).Scan(&env.productA)
	return env
}

func (e *m7Env) close() {
	e.srv.Close()
	e.pool.Close()
}

func newClient(t *testing.T) *http.Client {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar}
}

func (e *m7Env) login(t *testing.T, c *http.Client, email, pass string) sessionBody {
	t.Helper()
	resp := e.do(t, c, http.MethodPost, "/auth/login", map[string]string{"email": email, "password": pass})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login %s: esperaba 200, obtuvo %d", email, resp.StatusCode)
	}
	var sb sessionBody
	decode(t, resp, &sb)
	return sb
}

func (e *m7Env) do(t *testing.T, c *http.Client, method, path string, body any) *http.Response {
	t.Helper()
	var r *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	} else {
		r = bytes.NewReader(nil)
	}
	req, _ := http.NewRequest(method, e.srv.URL+path, r)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	return resp
}

func decode(t *testing.T, resp *http.Response, v any) {
	t.Helper()
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		t.Fatalf("decode: %v", err)
	}
}

type sessionBody struct {
	User             auth.User        `json:"user"`
	Tenant           *auth.MeTenant   `json:"tenant"`
	Branches         []auth.BranchRef `json:"branches"`
	ActiveBranchID   *string          `json:"activeBranchId"`
	MustSelectBranch bool             `json:"mustSelectBranch"`
}

// createBranch crea una sucursal vía HTTP como super admin y devuelve su id.
func (e *m7Env) createBranch(t *testing.T, c *http.Client, name string) string {
	t.Helper()
	resp := e.do(t, c, http.MethodPost, "/branches", map[string]string{"name": name})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /branches %s: esperaba 201, obtuvo %d", name, resp.StatusCode)
	}
	var br struct {
		Branch branches.Branch `json:"branch"`
	}
	decode(t, resp, &br)
	return br.Branch.ID
}

func TestM7V2Flow(t *testing.T) {
	env := setupM7(t)
	defer env.close()

	root := newClient(t)
	rootSession := env.login(t, root, "root@faro.test", "secret123")
	if rootSession.Tenant != nil || len(rootSession.Branches) != 0 || rootSession.MustSelectBranch {
		t.Fatalf("super admin: esperaba tenant nil, sin sucursales, sin selección; obtuvo %+v", rootSession)
	}

	// Super admin fija el favicon del negocio.
	if resp := env.do(t, root, http.MethodPut, "/settings/favicon", map[string]string{"faviconUrl": "/files/marca.webp"}); resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT /settings/favicon: esperaba 200, obtuvo %d", resp.StatusCode)
	}

	// Super admin crea sucursales (CRUD super-admin).
	b1 := env.createBranch(t, root, "Centro")
	b2 := env.createBranch(t, root, "Norte")
	b3 := env.createBranch(t, root, "Sur") // el cajero NO será miembro de esta

	// Alta del cajero con 2 sucursales (M:N).
	resp := env.do(t, root, http.MethodPost, "/users", map[string]any{
		"email": "cajero@vanta.test", "password": "secret123", "name": "Cajero", "branchIds": []string{b1, b2},
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /users: esperaba 201, obtuvo %d", resp.StatusCode)
	}
	var cu struct {
		User auth.User `json:"user"`
	}
	decode(t, resp, &cu)
	if len(cu.User.Branches) != 2 {
		t.Fatalf("cajero esperaba 2 sucursales, obtuvo %+v", cu.User.Branches)
	}

	// --- Login del cajero: >1 sucursal => mustSelectBranch, sin activa ---------
	cajero := newClient(t)
	sess := env.login(t, cajero, "cajero@vanta.test", "secret123")
	if len(sess.Branches) != 2 || sess.ActiveBranchID != nil || !sess.MustSelectBranch {
		t.Fatalf("cajero login: esperaba 2 sucursales, sin activa, mustSelect=true; obtuvo %+v", sess)
	}
	if sess.Tenant == nil || sess.Tenant.FaviconURL == nil || *sess.Tenant.FaviconURL != "/files/marca.webp" {
		t.Fatalf("cajero login tenant/favicon inesperado: %+v", sess.Tenant)
	}

	// Sin sucursal activa: POST /sales => 400 branch_required.
	if resp := env.do(t, cajero, http.MethodPost, "/sales", map[string]any{
		"items": []map[string]any{{"productId": env.productA, "quantity": 1}}, "paymentMethod": "cash", "amountPaidCents": 5000,
	}); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("venta sin sucursal activa: esperaba 400, obtuvo %d", resp.StatusCode)
	}

	// --- select-branch: membresía inválida y válida ---------------------------
	if resp := env.do(t, cajero, http.MethodPost, "/auth/select-branch", map[string]string{"branchId": b3}); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("select-branch no miembro: esperaba 403, obtuvo %d", resp.StatusCode)
	}
	if resp := env.do(t, cajero, http.MethodPost, "/auth/select-branch", map[string]string{"branchId": "00000000-0000-0000-0000-000000000000"}); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("select-branch inexistente: esperaba 404, obtuvo %d", resp.StatusCode)
	}
	resp = env.do(t, cajero, http.MethodPost, "/auth/select-branch", map[string]string{"branchId": b1})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("select-branch válido: esperaba 200, obtuvo %d", resp.StatusCode)
	}
	var sel struct {
		ActiveBranchID string `json:"activeBranchId"`
	}
	decode(t, resp, &sel)
	if sel.ActiveBranchID != b1 {
		t.Fatalf("select-branch activeBranchId esperaba %s, obtuvo %s", b1, sel.ActiveBranchID)
	}

	// --- La venta deriva branch_id de la sucursal activa (b1) ------------------
	resp = env.do(t, cajero, http.MethodPost, "/sales", map[string]any{
		"items": []map[string]any{{"productId": env.productA, "quantity": 1}}, "paymentMethod": "cash", "amountPaidCents": 5000,
		"branchId": b2, // intento de forzar otra sucursal: se ignora
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /sales: esperaba 201, obtuvo %d", resp.StatusCode)
	}
	var sc struct {
		Sale sales.Sale `json:"sale"`
	}
	decode(t, resp, &sc)
	if sc.Sale.BranchID == nil || *sc.Sale.BranchID != b1 {
		t.Fatalf("venta debía derivar b1, obtuvo %+v", sc.Sale.BranchID)
	}

	// /auth/me refleja la sucursal activa y el favicon.
	resp = env.do(t, cajero, http.MethodGet, "/auth/me", nil)
	var me sessionBody
	decode(t, resp, &me)
	if me.ActiveBranchID == nil || *me.ActiveBranchID != b1 || me.MustSelectBranch {
		t.Fatalf("/auth/me: esperaba activa b1 y mustSelect=false; obtuvo %+v", me)
	}

	// GET /sales se fuerza a la sucursal activa (b1): 1 venta.
	resp = env.do(t, cajero, http.MethodGet, "/sales", nil)
	var listB1 struct {
		Items []sales.Sale `json:"items"`
	}
	decode(t, resp, &listB1)
	if len(listB1.Items) != 1 {
		t.Fatalf("GET /sales en b1 esperaba 1 venta, obtuvo %d", len(listB1.Items))
	}

	// Cambia a b2 y vende: GET /sales de b2 muestra solo la venta de b2.
	env.do(t, cajero, http.MethodPost, "/auth/select-branch", map[string]string{"branchId": b2}).Body.Close()
	resp = env.do(t, cajero, http.MethodPost, "/sales", map[string]any{
		"items": []map[string]any{{"productId": env.productA, "quantity": 2}}, "paymentMethod": "card", "amountPaidCents": 0,
	})
	decode(t, resp, &sc)
	if sc.Sale.BranchID == nil || *sc.Sale.BranchID != b2 {
		t.Fatalf("venta en b2 esperaba branch b2, obtuvo %+v", sc.Sale.BranchID)
	}
	resp = env.do(t, cajero, http.MethodGet, "/sales", nil)
	var listB2 struct {
		Items []sales.Sale `json:"items"`
	}
	decode(t, resp, &listB2)
	if len(listB2.Items) != 1 || listB2.Items[0].BranchID == nil || *listB2.Items[0].BranchID != b2 {
		t.Fatalf("GET /sales en b2 esperaba 1 venta de b2, obtuvo %+v", listB2.Items)
	}

	// --- Autorización: el cajero NO accede a admin ----------------------------
	if resp := env.do(t, cajero, http.MethodPost, "/branches", map[string]string{"name": "Hack"}); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cajero POST /branches: esperaba 403, obtuvo %d", resp.StatusCode)
	}
	if resp := env.do(t, cajero, http.MethodGet, "/reports/sales", nil); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cajero GET /reports: esperaba 403, obtuvo %d", resp.StatusCode)
	}
	if resp := env.do(t, cajero, http.MethodPost, "/products", map[string]any{"name": "X", "priceCents": 100}); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cajero POST /products: esperaba 403, obtuvo %d", resp.StatusCode)
	}
	// Lectura de catálogo SÍ permitida al cajero.
	if resp := env.do(t, cajero, http.MethodGet, "/products", nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("cajero GET /products: esperaba 200, obtuvo %d", resp.StatusCode)
	}

	// --- Reportes (super admin): byBranch con b1 y b2 -------------------------
	from := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	to := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	resp = env.do(t, root, http.MethodGet, "/reports/sales?from="+from+"&to="+to, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("super admin GET /reports: esperaba 200, obtuvo %d", resp.StatusCode)
	}
	var rep reports.SalesReport
	decode(t, resp, &rep)
	seen := map[string]bool{}
	for _, b := range rep.ByBranch {
		if b.BranchID != nil {
			seen[*b.BranchID] = true
		}
	}
	if !seen[b1] || !seen[b2] {
		t.Fatalf("byBranch esperaba b1 y b2, obtuvo %+v", rep.ByBranch)
	}

	// DELETE /branches en uso (b1 tiene membresía y ventas) => 409.
	if resp := env.do(t, root, http.MethodDelete, "/branches/"+b1, nil); resp.StatusCode != http.StatusConflict {
		t.Fatalf("DELETE /branches en uso: esperaba 409, obtuvo %d", resp.StatusCode)
	}
}
