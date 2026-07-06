package server_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"faro/internal/auth"
	"faro/internal/reports"
)

// createUser da de alta un usuario vía HTTP como super admin y devuelve status + user.
func (e *m7Env) createUser(t *testing.T, c *http.Client, body map[string]any) (int, auth.User) {
	t.Helper()
	resp := e.do(t, c, http.MethodPost, "/users", body)
	if resp.StatusCode != http.StatusCreated {
		resp.Body.Close()
		return resp.StatusCode, auth.User{}
	}
	var cu struct {
		User auth.User `json:"user"`
	}
	decode(t, resp, &cu)
	return resp.StatusCode, cu.User
}

// TestM8UserRoles cubre el alta de usuarios por rol (M8): un usuario por rol de
// sucursal, el super admin global, y los rechazos (rol inválido, super_admin con
// branchIds, rol de sucursal sin branchIds).
func TestM8UserRoles(t *testing.T) {
	env := setupM7(t)
	defer env.close()

	root := newClient(t)
	env.login(t, root, "root@faro.test", "secret123")
	b1 := env.createBranch(t, root, "Centro")

	// Un usuario por cada rol de sucursal.
	for i, role := range []string{"branch_admin", "cashier", "barista"} {
		email := role + "@vanta.test"
		status, u := env.createUser(t, root, map[string]any{
			"email": email, "password": "secret123", "name": role, "role": role, "branchIds": []string{b1},
		})
		if status != http.StatusCreated {
			t.Fatalf("crear %s: esperaba 201, obtuvo %d", role, status)
		}
		if u.Role != role || u.IsSuperAdmin || u.TenantID == nil || len(u.Branches) != 1 {
			t.Fatalf("caso %d (%s): usuario inesperado %+v", i, role, u)
		}
	}

	// Super admin global: sin branchIds, tenant NULL, is_super_admin true.
	status, sa := env.createUser(t, root, map[string]any{
		"email": "sa2@faro.test", "password": "secret123", "name": "SA2", "role": "super_admin",
	})
	if status != http.StatusCreated {
		t.Fatalf("crear super_admin: esperaba 201, obtuvo %d", status)
	}
	if sa.Role != "super_admin" || !sa.IsSuperAdmin || sa.TenantID != nil || len(sa.Branches) != 0 {
		t.Fatalf("super_admin creado inesperado: %+v", sa)
	}

	// Rechazos (400 validation_error).
	rejects := []map[string]any{
		{"email": "bad1@vanta.test", "password": "secret123", "name": "Bad", "role": "manager", "branchIds": []string{b1}},    // rol inválido
		{"email": "bad2@faro.test", "password": "secret123", "name": "Bad", "role": "super_admin", "branchIds": []string{b1}}, // super_admin con branchIds
		{"email": "bad3@vanta.test", "password": "secret123", "name": "Bad", "role": "cashier"},                               // rol de sucursal sin branchIds
	}
	for i, body := range rejects {
		resp := env.do(t, root, http.MethodPost, "/users", body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("rechazo %d: esperaba 400, obtuvo %d", i, resp.StatusCode)
		}
	}

	// PATCH: cambio entre roles de sucursal permitido; promover a super_admin rechazado.
	// Reutilizamos el cashier: cashier -> barista OK.
	var cashierID string
	env.pool.QueryRow(context.Background(),
		"SELECT id::text FROM users WHERE email='cashier@vanta.test'").Scan(&cashierID)
	if resp := env.do(t, root, http.MethodPatch, "/users/"+cashierID, map[string]any{"role": "barista"}); resp.StatusCode != http.StatusOK {
		t.Fatalf("PATCH cashier->barista: esperaba 200, obtuvo %d", resp.StatusCode)
	}
	if resp := env.do(t, root, http.MethodPatch, "/users/"+cashierID, map[string]any{"role": "super_admin"}); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("PATCH ->super_admin: esperaba 400, obtuvo %d", resp.StatusCode)
	}
}

// TestM8ReportsByRole verifica la matriz de /reports por rol: super admin ve todas
// las sucursales; branch_admin queda forzado a su sucursal activa (ignora ?branchId
// ajeno); sin sucursal activa => 400; cashier => 403.
func TestM8ReportsByRole(t *testing.T) {
	env := setupM7(t)
	defer env.close()
	ctx := context.Background()

	root := newClient(t)
	env.login(t, root, "root@faro.test", "secret123")
	b1 := env.createBranch(t, root, "Centro")
	b2 := env.createBranch(t, root, "Norte")

	// Ventas: b1 = 10000, b2 = 5000 (total negocio = 15000).
	env.pool.Exec(ctx, "INSERT INTO sales (tenant_id,branch_id,total_cents,amount_paid_cents,change_cents,payment_method) VALUES ($1,$2,10000,10000,0,'cash')", env.tenantID, b1)
	env.pool.Exec(ctx, "INSERT INTO sales (tenant_id,branch_id,total_cents,amount_paid_cents,change_cents,payment_method) VALUES ($1,$2,5000,5000,0,'card')", env.tenantID, b2)

	// branch_admin de una sola sucursal (b1): al loguear queda activa b1.
	env.createUser(t, root, map[string]any{
		"email": "ba@vanta.test", "password": "secret123", "name": "BA", "role": "branch_admin", "branchIds": []string{b1},
	})
	// branch_admin de dos sucursales (b1,b2): mustSelect => sin sucursal activa.
	env.createUser(t, root, map[string]any{
		"email": "ba2@vanta.test", "password": "secret123", "name": "BA2", "role": "branch_admin", "branchIds": []string{b1, b2},
	})
	// cashier de b1.
	env.createUser(t, root, map[string]any{
		"email": "cash@vanta.test", "password": "secret123", "name": "Cash", "role": "cashier", "branchIds": []string{b1},
	})

	from := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	to := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	q := "?from=" + from + "&to=" + to

	// Super admin: ve las dos sucursales, total 15000.
	resp := env.do(t, root, http.MethodGet, "/reports/sales"+q, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("super admin /reports: esperaba 200, obtuvo %d", resp.StatusCode)
	}
	var repRoot reports.SalesReport
	decode(t, resp, &repRoot)
	if repRoot.TotalCents != 15000 {
		t.Fatalf("super admin total: esperaba 15000, obtuvo %d", repRoot.TotalCents)
	}
	seen := map[string]bool{}
	for _, b := range repRoot.ByBranch {
		if b.BranchID != nil {
			seen[*b.BranchID] = true
		}
	}
	if !seen[b1] || !seen[b2] {
		t.Fatalf("super admin byBranch: esperaba b1 y b2, obtuvo %+v", repRoot.ByBranch)
	}

	// branch_admin (activa b1): forzado a b1 aunque pida ?branchId=b2 => total 10000.
	ba := newClient(t)
	baSess := env.login(t, ba, "ba@vanta.test", "secret123")
	if baSess.ActiveBranchID == nil || *baSess.ActiveBranchID != b1 {
		t.Fatalf("branch_admin login: esperaba activa b1, obtuvo %+v", baSess.ActiveBranchID)
	}
	resp = env.do(t, ba, http.MethodGet, "/reports/sales"+q+"&branchId="+b2, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("branch_admin /reports: esperaba 200, obtuvo %d", resp.StatusCode)
	}
	var repBA reports.SalesReport
	decode(t, resp, &repBA)
	if repBA.TotalCents != 10000 {
		t.Fatalf("branch_admin total: esperaba 10000 (forzado a b1, ignora ?branchId=b2), obtuvo %d", repBA.TotalCents)
	}

	// branch_admin sin sucursal activa (2 sucursales, mustSelect) => 400 branch_required.
	ba2 := newClient(t)
	ba2Sess := env.login(t, ba2, "ba2@vanta.test", "secret123")
	if ba2Sess.ActiveBranchID != nil || !ba2Sess.MustSelectBranch {
		t.Fatalf("branch_admin 2 sucursales: esperaba sin activa + mustSelect, obtuvo %+v", ba2Sess)
	}
	if resp := env.do(t, ba2, http.MethodGet, "/reports/sales"+q, nil); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("branch_admin sin activa /reports: esperaba 400, obtuvo %d", resp.StatusCode)
	}

	// cashier => 403.
	cash := newClient(t)
	env.login(t, cash, "cash@vanta.test", "secret123")
	if resp := env.do(t, cash, http.MethodGet, "/reports/sales"+q, nil); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cashier /reports: esperaba 403, obtuvo %d", resp.StatusCode)
	}
}
