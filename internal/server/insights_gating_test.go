package server_test

import (
	"context"
	"net/http"
	"testing"
	"time"
)

// insights_gating_test.go — Q1 (gating & scope) de Insights. Ejercita el
// middleware de auth REAL + resolveScope sobre las 6 rutas /insights/*:
//   - sin sesión => 401
//   - cashier / barista => 403 (en las 6)
//   - super_admin => 200 (con branchId, branchId=none o sin param)
//   - branch_admin sin sucursal activa => 400 branch_required
//   - branch_admin con activa => forzado a su sucursal (ignora ?branchId)
//   - estados vacíos => 200 (no 500), habilita la carga aislada por tarjeta (Q4)
// Reutiliza los helpers de m7_integration_test.go (mismo paquete server_test).

var insightsRoutes = []string{
	"/insights/recurrence",
	"/insights/top-products",
	"/insights/ticket-segments",
	"/insights/second-visit",
	"/insights/basket-affinity",
	"/insights/loyalty-effect",
}

// newInsightsUser da de alta un usuario con rol y sucursales (reusa createUser).
func newInsightsUser(t *testing.T, env *m7Env, root *http.Client, email, role string, branchIDs []string) {
	t.Helper()
	status, _ := env.createUser(t, root, map[string]any{
		"email": email, "password": "secret123", "name": role, "role": role, "branchIds": branchIDs,
	})
	if status != http.StatusCreated {
		t.Fatalf("POST /users (%s): esperaba 201, obtuvo %d", role, status)
	}
}

func TestInsightsGating(t *testing.T) {
	env := setupM7(t)
	defer env.close()

	root := newClient(t)
	env.login(t, root, "root@faro.test", "secret123")
	b1 := env.createBranch(t, root, "Centro")
	b2 := env.createBranch(t, root, "Norte")

	// Usuarios de cada rol. branch_admin con 2 sucursales => login sin activa.
	newInsightsUser(t, env, root, "cajero@vanta.test", "cashier", []string{b1})
	newInsightsUser(t, env, root, "barista@vanta.test", "barista", []string{b1})
	newInsightsUser(t, env, root, "admin@vanta.test", "branch_admin", []string{b1, b2})

	// --- Sin sesión => 401 en las 6 rutas -----------------------------------
	anon := newClient(t)
	for _, route := range insightsRoutes {
		resp := env.do(t, anon, http.MethodGet, route, nil)
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("sin sesión %s: esperaba 401, obtuvo %d", route, resp.StatusCode)
		}
	}

	// --- cashier y barista => 403 en las 6 rutas ----------------------------
	for _, u := range []string{"cajero@vanta.test", "barista@vanta.test"} {
		c := newClient(t)
		env.login(t, c, u, "secret123")
		for _, route := range insightsRoutes {
			resp := env.do(t, c, http.MethodGet, route, nil)
			resp.Body.Close()
			if resp.StatusCode != http.StatusForbidden {
				t.Fatalf("%s %s: esperaba 403, obtuvo %d", u, route, resp.StatusCode)
			}
		}
	}

	// --- super_admin => 200 en las 6 (sin datos: estado vacío, no 500) ------
	for _, route := range insightsRoutes {
		resp := env.do(t, root, http.MethodGet, route, nil)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("super_admin %s: esperaba 200 (estado vacío), obtuvo %d", route, resp.StatusCode)
		}
	}
	// super_admin con branchId=<uuid> y branchId=none => 200.
	for _, q := range []string{"?branchId=" + b1, "?branchId=none"} {
		resp := env.do(t, root, http.MethodGet, "/insights/recurrence"+q, nil)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("super_admin recurrence %s: esperaba 200, obtuvo %d", q, resp.StatusCode)
		}
	}

	// --- branch_admin SIN sucursal activa => 400 branch_required ------------
	admin := newClient(t)
	adminSess := env.login(t, admin, "admin@vanta.test", "secret123")
	if adminSess.ActiveBranchID != nil {
		t.Fatalf("branch_admin con 2 sucursales esperaba sin activa, obtuvo %v", adminSess.ActiveBranchID)
	}
	for _, route := range insightsRoutes {
		resp := env.do(t, admin, http.MethodGet, route, nil)
		var body struct {
			Code string `json:"code"`
		}
		decode(t, resp, &body)
		if resp.StatusCode != http.StatusBadRequest || body.Code != "branch_required" {
			t.Fatalf("branch_admin sin sucursal %s: esperaba 400 branch_required, obtuvo %d/%q", route, resp.StatusCode, body.Code)
		}
	}

	// --- branch_admin CON activa => forzado a su sucursal, ignora ?branchId --
	// Sembramos un cliente recurrente (2 ventas) SOLO en b1; nada en b2.
	ctx := context.Background()
	var cust string
	if err := env.pool.QueryRow(ctx,
		"INSERT INTO customers (tenant_id, phone, first_name, last_name) VALUES ($1,'999','R','R') RETURNING id::text",
		env.tenantID).Scan(&cust); err != nil {
		t.Fatalf("seed cliente: %v", err)
	}
	now := time.Now()
	for i := 0; i < 2; i++ {
		if _, err := env.pool.Exec(ctx,
			"INSERT INTO sales (tenant_id, customer_id, branch_id, total_cents, amount_paid_cents, change_cents, payment_method, created_at) VALUES ($1,$2,$3,5000,5000,0,'cash',$4)",
			env.tenantID, cust, b1, now.AddDate(0, 0, -i*3)); err != nil {
			t.Fatalf("seed venta b1: %v", err)
		}
	}

	// Selecciona b1 como sucursal activa.
	sel := env.do(t, admin, http.MethodPost, "/auth/select-branch", map[string]string{"branchId": b1})
	if sel.StatusCode != http.StatusOK {
		t.Fatalf("select-branch b1: esperaba 200, obtuvo %d", sel.StatusCode)
	}
	sel.Body.Close()

	// Con activa: 200 en las 6.
	for _, route := range insightsRoutes {
		resp := env.do(t, admin, http.MethodGet, route, nil)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("branch_admin con activa %s: esperaba 200, obtuvo %d", route, resp.StatusCode)
		}
	}

	// Forzado: aunque pida ?branchId=b2 (sin datos), ve los datos de b1 (su activa).
	rangeQ := "&from=" + now.AddDate(0, 0, -30).UTC().Format(time.RFC3339) +
		"&to=" + now.AddDate(0, 0, 1).UTC().Format(time.RFC3339)
	resp := env.do(t, admin, http.MethodGet, "/insights/recurrence?branchId="+b2+rangeQ, nil)
	var rec struct {
		WithGe1 int  `json:"withGe1"`
		WithGe2 int  `json:"withGe2"`
		Empty   bool `json:"empty"`
	}
	decode(t, resp, &rec)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("branch_admin recurrence forzado: esperaba 200, obtuvo %d", resp.StatusCode)
	}
	// Si respetara ?branchId=b2 vería vacío; al estar forzado a b1 ve al recurrente.
	if rec.Empty || rec.WithGe1 != 1 || rec.WithGe2 != 1 {
		t.Fatalf("branch_admin forzado a b1 (ignora ?branchId=b2): esperaba withGe1=1 withGe2=1 empty=false, obtuvo %+v", rec)
	}
}
