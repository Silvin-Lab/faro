package server_test

import (
	"context"
	"net/http"
	"testing"

	"faro/internal/customers"
)

// createCustomer inserta un cliente directamente en la DB del negocio y devuelve su id.
func (e *m7Env) createCustomer(t *testing.T, phone, first, last string, visits, lifetime int) string {
	t.Helper()
	var id string
	err := e.pool.QueryRow(context.Background(),
		`INSERT INTO customers (tenant_id, phone, first_name, last_name, visits, visits_lifetime)
		 VALUES ($1,$2,$3,$4,$5,$6) RETURNING id::text`,
		e.tenantID, phone, first, last, visits, lifetime).Scan(&id)
	if err != nil {
		t.Fatalf("insert customer: %v", err)
	}
	return id
}

// setVisits llama a PATCH /customers/{id}/visits y devuelve status + cliente.
func (e *m7Env) setVisits(t *testing.T, c *http.Client, id string, body any) (int, customers.Customer) {
	t.Helper()
	resp := e.do(t, c, http.MethodPatch, "/customers/"+id+"/visits", body)
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return resp.StatusCode, customers.Customer{}
	}
	var out struct {
		Customer customers.Customer `json:"customer"`
	}
	decode(t, resp, &out)
	return resp.StatusCode, out.Customer
}

// TestCustomerVisitsAdjustment cubre el ajuste manual de visitas (migración de
// tarjetas físicas): autorización por rol, regla GREATEST del lifetime, validación
// y 404; además de que el super admin puede buscar y crear clientes.
func TestCustomerVisitsAdjustment(t *testing.T) {
	env := setupM7(t)
	defer env.close()

	root := newClient(t)
	env.login(t, root, "root@faro.test", "secret123")

	// Sucursal + branch_admin + cashier (roles de sucursal).
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

	// --- Super admin puede buscar y crear clientes (antes bloqueado por TenantOf) ---
	if resp := env.do(t, root, http.MethodGet, "/customers?q=juan", nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("super admin GET /customers?q: esperaba 200, obtuvo %d", resp.StatusCode)
	} else {
		resp.Body.Close()
	}
	resp := env.do(t, root, http.MethodPost, "/customers", map[string]string{
		"phone": "3001112233", "firstName": "Juan", "lastName": "Perez",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("super admin POST /customers: esperaba 201, obtuvo %d", resp.StatusCode)
	}
	var created struct {
		Customer customers.Customer `json:"customer"`
	}
	decode(t, resp, &created)
	if resp := env.do(t, root, http.MethodGet, "/customers?phone=3001112233", nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("super admin GET /customers?phone: esperaba 200, obtuvo %d", resp.StatusCode)
	} else {
		resp.Body.Close()
	}

	// --- Set visits como super admin: cliente nuevo (0/0) -> set 7 => 7/7 ---
	cNew := env.createCustomer(t, "3002223344", "Ana", "Gomez", 0, 0)
	status, cust := env.setVisits(t, root, cNew, map[string]int{"visits": 7})
	if status != http.StatusOK {
		t.Fatalf("super admin set visits=7: esperaba 200, obtuvo %d", status)
	}
	if cust.Visits != 7 || cust.VisitsLifetime != 7 {
		t.Fatalf("nuevo set 7: esperaba 7/7, obtuvo %d/%d", cust.Visits, cust.VisitsLifetime)
	}

	// --- Set visits como branch_admin: GREATEST conserva lifetime alto ---
	// Cliente con lifetime 10; set visits=3 => visits 3, lifetime 10 (no baja).
	cHi := env.createCustomer(t, "3003334455", "Beto", "Ruiz", 5, 10)
	status, cust = env.setVisits(t, admin, cHi, map[string]int{"visits": 3})
	if status != http.StatusOK {
		t.Fatalf("branch_admin set visits=3: esperaba 200, obtuvo %d", status)
	}
	if cust.Visits != 3 || cust.VisitsLifetime != 10 {
		t.Fatalf("GREATEST: esperaba 3/10, obtuvo %d/%d", cust.Visits, cust.VisitsLifetime)
	}

	// --- cashier -> 403 forbidden ---
	if status, _ := env.setVisits(t, cashier, cNew, map[string]int{"visits": 1}); status != http.StatusForbidden {
		t.Fatalf("cashier set visits: esperaba 403, obtuvo %d", status)
	}

	// --- visits negativo -> 400 ---
	if status, _ := env.setVisits(t, admin, cNew, map[string]int{"visits": -1}); status != http.StatusBadRequest {
		t.Fatalf("visits negativo: esperaba 400, obtuvo %d", status)
	}
	// --- visits ausente -> 400 ---
	if status, _ := env.setVisits(t, admin, cNew, map[string]string{"foo": "bar"}); status != http.StatusBadRequest {
		t.Fatalf("visits ausente: esperaba 400, obtuvo %d", status)
	}

	// --- Cliente inexistente (UUID válido, sin fila) -> 404 ---
	if status, _ := env.setVisits(t, admin, "00000000-0000-0000-0000-000000000000", map[string]int{"visits": 2}); status != http.StatusNotFound {
		t.Fatalf("cliente inexistente: esperaba 404, obtuvo %d", status)
	}
}
