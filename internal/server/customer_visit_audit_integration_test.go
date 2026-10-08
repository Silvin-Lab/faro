package server_test

import (
	"context"
	"net/http"
	"testing"

	"faro/internal/customers"
)

// visitChanges llama a GET /customers/{id}/visit-changes y devuelve status + items.
func (e *m7Env) visitChanges(t *testing.T, c *http.Client, id string) (int, []customers.VisitChange) {
	t.Helper()
	resp := e.do(t, c, http.MethodGet, "/customers/"+id+"/visit-changes", nil)
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return resp.StatusCode, nil
	}
	var out struct {
		Items []customers.VisitChange `json:"items"`
	}
	decode(t, resp, &out)
	return resp.StatusCode, out.Items
}

// createCustomerHTTP hace POST /customers y devuelve status + cliente.
func (e *m7Env) createCustomerHTTP(t *testing.T, c *http.Client, body map[string]any) (int, customers.Customer) {
	t.Helper()
	resp := e.do(t, c, http.MethodPost, "/customers", body)
	if resp.StatusCode != http.StatusCreated {
		resp.Body.Close()
		return resp.StatusCode, customers.Customer{}
	}
	var out struct {
		Customer customers.Customer `json:"customer"`
	}
	decode(t, resp, &out)
	return resp.StatusCode, out.Customer
}

// TestCustomerPriorVisitsAuthzAndAudit cubre la restricción de priorVisits por rol
// y la auditoría de visitas (alta y ajuste), incluido el aislamiento por tenant.
func TestCustomerPriorVisitsAuthzAndAudit(t *testing.T) {
	env := setupM7(t)
	defer env.close()
	ctx := context.Background()

	root := newClient(t)
	env.login(t, root, "root@faro.test", "secret123")

	b1 := env.createBranch(t, root, "Centro")
	env.do(t, root, http.MethodPost, "/users", map[string]any{
		"email": "admin@vanta.test", "password": "secret123", "name": "Admin", "role": "branch_admin", "branchIds": []string{b1},
	}).Body.Close()
	env.do(t, root, http.MethodPost, "/users", map[string]any{
		"email": "cajero@vanta.test", "password": "secret123", "name": "Cajero", "role": "cashier", "branchIds": []string{b1},
	}).Body.Close()

	admin := newClient(t)
	env.login(t, admin, "admin@vanta.test", "secret123")
	cashier := newClient(t)
	env.login(t, cashier, "cajero@vanta.test", "secret123")

	// --- Cajero con priorVisits > 0 => 403 prior_visits_forbidden, sin crear nada ---
	resp := env.do(t, cashier, http.MethodPost, "/customers", map[string]any{
		"phone": "3001110000", "firstName": "Fal", "lastName": "So", "priorVisits": 5,
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cajero priorVisits>0: esperaba 403, obtuvo %d", resp.StatusCode)
	}
	var errBody struct{ Code string }
	decode(t, resp, &errBody)
	if errBody.Code != "prior_visits_forbidden" {
		t.Fatalf("código esperaba prior_visits_forbidden, obtuvo %q", errBody.Code)
	}
	var n int
	env.pool.QueryRow(ctx, "SELECT count(*) FROM customers WHERE phone='3001110000'").Scan(&n)
	if n != 0 {
		t.Fatalf("no debía crearse cliente, hay %d", n)
	}

	// --- Cajero con priorVisits = 0 => OK; createdByName = Cajero ---
	status, cust := env.createCustomerHTTP(t, cashier, map[string]any{
		"phone": "3001112222", "firstName": "Bea", "lastName": "Luz", "priorVisits": 0,
	})
	if status != http.StatusCreated {
		t.Fatalf("cajero priorVisits=0: esperaba 201, obtuvo %d", status)
	}
	if cust.Visits != 0 || cust.CreatedByName == nil || *cust.CreatedByName != "Cajero" {
		t.Fatalf("alta cajero: visits=%d createdByName=%v", cust.Visits, cust.CreatedByName)
	}
	// Un alta con 0 visitas no genera auditoría.
	if _, items := env.visitChanges(t, admin, cust.ID); len(items) != 0 {
		t.Fatalf("alta con 0 visitas no debía auditar, hay %d filas", len(items))
	}

	// --- Admin con priorVisits = 7 => OK; createdByName = Admin; auditoría 'create' ---
	status, adminCust := env.createCustomerHTTP(t, admin, map[string]any{
		"phone": "3003334444", "firstName": "Ana", "lastName": "Paz", "priorVisits": 7,
	})
	if status != http.StatusCreated {
		t.Fatalf("admin priorVisits=7: esperaba 201, obtuvo %d", status)
	}
	if adminCust.Visits != 7 || adminCust.VisitsLifetime != 7 || adminCust.CreatedByName == nil || *adminCust.CreatedByName != "Admin" {
		t.Fatalf("alta admin: visits=%d/%d createdByName=%v", adminCust.Visits, adminCust.VisitsLifetime, adminCust.CreatedByName)
	}
	st, items := env.visitChanges(t, admin, adminCust.ID)
	if st != http.StatusOK || len(items) != 1 {
		t.Fatalf("auditoría create: status=%d filas=%d (esperaba 200/1)", st, len(items))
	}
	if items[0].Source != "create" || items[0].VisitsBefore != 0 || items[0].VisitsAfter != 7 || items[0].ByName == nil || *items[0].ByName != "Admin" {
		t.Fatalf("fila create inesperada: %+v", items[0])
	}

	// --- PATCH visits (admin) escribe 'adjust'; historial más reciente primero ---
	if s, _ := env.setVisits(t, admin, adminCust.ID, map[string]int{"visits": 3}); s != http.StatusOK {
		t.Fatalf("PATCH visits: esperaba 200, obtuvo %d", s)
	}
	_, items = env.visitChanges(t, admin, adminCust.ID)
	if len(items) != 2 {
		t.Fatalf("auditoría tras ajuste: esperaba 2 filas, obtuvo %d", len(items))
	}
	if items[0].Source != "adjust" || items[0].VisitsBefore != 7 || items[0].VisitsAfter != 3 || items[0].ByName == nil || *items[0].ByName != "Admin" {
		t.Fatalf("fila adjust (más reciente) inesperada: %+v", items[0])
	}
	if items[1].Source != "create" {
		t.Fatalf("segunda fila esperaba 'create', obtuvo %q", items[1].Source)
	}

	// --- Cajero no puede ver el historial => 403 ---
	if st, _ := env.visitChanges(t, cashier, adminCust.ID); st != http.StatusForbidden {
		t.Fatalf("cajero visit-changes: esperaba 403, obtuvo %d", st)
	}

	// --- Aislamiento por tenant: historial de un cliente de OTRO negocio => 404 ---
	var otherTenant, otherCust string
	env.pool.QueryRow(ctx, "INSERT INTO tenants (name) VALUES ('Otro') RETURNING id::text").Scan(&otherTenant)
	env.pool.QueryRow(ctx,
		"INSERT INTO customers (tenant_id, phone, first_name, last_name, visits, visits_lifetime) VALUES ($1,'9','X','Y',1,1) RETURNING id::text",
		otherTenant).Scan(&otherCust)
	if st, _ := env.visitChanges(t, admin, otherCust); st != http.StatusNotFound {
		t.Fatalf("visit-changes cross-tenant: esperaba 404, obtuvo %d", st)
	}
}
