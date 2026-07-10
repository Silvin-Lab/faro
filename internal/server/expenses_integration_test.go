package server_test

import (
	"context"
	"net/http"
	"testing"
	"time"
)

// newBranchUser da de alta un usuario de sucursal como super admin (reutiliza el
// helper createUser de m8) y devuelve su id.
func (e *m7Env) newBranchUser(t *testing.T, c *http.Client, email, role string, branchIDs []string) string {
	t.Helper()
	status, u := e.createUser(t, c, map[string]any{
		"email": email, "password": "secret123", "name": email, "role": role, "branchIds": branchIDs,
	})
	if status != http.StatusCreated {
		t.Fatalf("POST /users %s: esperaba 201, obtuvo %d", email, status)
	}
	return u.ID
}

// selectBranch fija la sucursal activa de la sesión.
func (e *m7Env) selectBranch(t *testing.T, c *http.Client, branchID string) {
	t.Helper()
	resp := e.do(t, c, http.MethodPost, "/auth/select-branch", map[string]string{"branchId": branchID})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("select-branch %s: esperaba 200, obtuvo %d", branchID, resp.StatusCode)
	}
	resp.Body.Close()
}

// createExpense registra un gasto vía HTTP y devuelve su id (o falla).
func (e *m7Env) createExpense(t *testing.T, c *http.Client, conceptID string, amount int) string {
	t.Helper()
	resp := e.do(t, c, http.MethodPost, "/expenses", map[string]any{"conceptId": conceptID, "amountCents": amount})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /expenses: esperaba 201, obtuvo %d", resp.StatusCode)
	}
	var body struct {
		Expense struct {
			ID string `json:"id"`
		} `json:"expense"`
	}
	decode(t, resp, &body)
	return body.Expense.ID
}

func TestExpensesHTTP(t *testing.T) {
	env := setupM7(t)
	defer env.close()
	ctx := context.Background()

	root := newClient(t)
	env.login(t, root, "root@faro.test", "secret123")

	b1 := env.createBranch(t, root, "Centro")
	b2 := env.createBranch(t, root, "Norte")

	// --- Super admin crea catálogo -------------------------------------------
	resp := env.do(t, root, http.MethodPost, "/expenses/categories", map[string]any{"name": "Servicios"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /expenses/categories: esperaba 201, obtuvo %d", resp.StatusCode)
	}
	var catBody struct {
		Category struct {
			ID string `json:"id"`
		} `json:"category"`
	}
	decode(t, resp, &catBody)

	resp = env.do(t, root, http.MethodPost, "/expenses/concepts", map[string]any{"name": "Luz", "categoryId": catBody.Category.ID})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /expenses/concepts: esperaba 201, obtuvo %d", resp.StatusCode)
	}
	var conBody struct {
		Concept struct {
			ID           string  `json:"id"`
			CategoryName *string `json:"categoryName"`
		} `json:"concept"`
	}
	decode(t, resp, &conBody)
	if conBody.Concept.CategoryName == nil || *conBody.Concept.CategoryName != "Servicios" {
		t.Fatalf("concepto debía traer categoryName Servicios, obtuvo %+v", conBody.Concept.CategoryName)
	}
	conceptID := conBody.Concept.ID

	// --- Usuarios de sucursal -------------------------------------------------
	cashierID := env.newBranchUser(t, root, "cajero@vanta.test", "cashier", []string{b1, b2})
	env.newBranchUser(t, root, "admin@vanta.test", "branch_admin", []string{b1, b2})

	// --- Cajero: authz de catálogo (escritura => 403) -------------------------
	cashier := newClient(t)
	env.login(t, cashier, "cajero@vanta.test", "secret123")
	if resp := env.do(t, cashier, http.MethodPost, "/expenses/categories", map[string]any{"name": "Hack"}); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cajero POST /expenses/categories: esperaba 403, obtuvo %d", resp.StatusCode)
	}
	if resp := env.do(t, cashier, http.MethodPost, "/expenses/concepts", map[string]any{"name": "Hack"}); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cajero POST /expenses/concepts: esperaba 403, obtuvo %d", resp.StatusCode)
	}
	// Lectura de catálogo SÍ permitida.
	if resp := env.do(t, cashier, http.MethodGet, "/expenses/concepts", nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("cajero GET /expenses/concepts: esperaba 200, obtuvo %d", resp.StatusCode)
	}

	// --- Cajero sin sucursal activa (2 sucursales) => 400 branch_required ------
	resp = env.do(t, cashier, http.MethodPost, "/expenses", map[string]any{"conceptId": conceptID, "amountCents": 100})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("gasto sin sucursal: esperaba 400, obtuvo %d", resp.StatusCode)
	}
	var perr struct {
		Code string `json:"code"`
	}
	decode(t, resp, &perr)
	if perr.Code != "branch_required" {
		t.Fatalf("gasto sin sucursal: esperaba code branch_required, obtuvo %q", perr.Code)
	}

	// --- Cajero selecciona b1 y registra un gasto -----------------------------
	env.selectBranch(t, cashier, b1)
	e1 := env.createExpense(t, cashier, conceptID, 15000)

	// Concepto inexistente => invalid_concept.
	resp = env.do(t, cashier, http.MethodPost, "/expenses", map[string]any{"conceptId": "00000000-0000-0000-0000-000000000000", "amountCents": 100})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("gasto concepto inexistente: esperaba 400, obtuvo %d", resp.StatusCode)
	}
	decode(t, resp, &perr)
	if perr.Code != "invalid_concept" {
		t.Fatalf("esperaba invalid_concept, obtuvo %q", perr.Code)
	}

	// GET /expenses del cajero => forzado a b1 (ve su gasto).
	resp = env.do(t, cashier, http.MethodGet, "/expenses", nil)
	var listBody struct {
		Items []struct {
			ID       string `json:"id"`
			BranchID string `json:"branchId"`
		} `json:"items"`
	}
	decode(t, resp, &listBody)
	if len(listBody.Items) != 1 || listBody.Items[0].BranchID != b1 {
		t.Fatalf("GET /expenses cajero: esperaba 1 gasto en b1, obtuvo %+v", listBody.Items)
	}

	// --- Admin registra un gasto en b2 (para el caso "otra sucursal") ---------
	admin := newClient(t)
	env.login(t, admin, "admin@vanta.test", "secret123")
	env.selectBranch(t, admin, b2)
	e2 := env.createExpense(t, admin, conceptID, 5000)

	// --- Matriz DELETE --------------------------------------------------------
	// Cajero NO puede borrar un gasto de otra sucursal (b2) => 403.
	if resp := env.do(t, cashier, http.MethodDelete, "/expenses/"+e2, nil); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cajero DELETE gasto de b2: esperaba 403, obtuvo %d", resp.StatusCode)
	}

	// Gasto viejo (>24h) del cajero en b1: se inserta backdateado directo en DB.
	var oldExpense string
	if err := env.pool.QueryRow(ctx,
		`INSERT INTO expenses (tenant_id, branch_id, concept_id, concept_name, amount_cents, created_by, created_at)
		 VALUES ($1,$2,$3,'Luz',2000,$4, now() - interval '48 hours') RETURNING id::text`,
		env.tenantID, b1, conceptID, cashierID).Scan(&oldExpense); err != nil {
		t.Fatalf("insert gasto viejo: %v", err)
	}
	// Cajero NO puede borrar gasto fuera de la ventana de 24h => 403.
	if resp := env.do(t, cashier, http.MethodDelete, "/expenses/"+oldExpense, nil); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cajero DELETE gasto >24h: esperaba 403, obtuvo %d", resp.StatusCode)
	}

	// Cajero SÍ puede borrar su gasto de hoy en su sucursal => 200.
	if resp := env.do(t, cashier, http.MethodDelete, "/expenses/"+e1, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("cajero DELETE gasto propio de hoy: esperaba 200, obtuvo %d", resp.StatusCode)
	}
	// Borrar de nuevo => 404 not_found.
	if resp := env.do(t, cashier, http.MethodDelete, "/expenses/"+e1, nil); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("cajero DELETE re-borrado: esperaba 404, obtuvo %d", resp.StatusCode)
	}

	// Branch_admin borra cualquier gasto de su sucursal activa (b2) => 200.
	if resp := env.do(t, admin, http.MethodDelete, "/expenses/"+e2, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("admin DELETE gasto de su sucursal: esperaba 200, obtuvo %d", resp.StatusCode)
	}

	// Super admin borra cualquiera del tenant (el gasto viejo) => 200.
	if resp := env.do(t, root, http.MethodDelete, "/expenses/"+oldExpense, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("super admin DELETE cualquiera: esperaba 200, obtuvo %d", resp.StatusCode)
	}

	// --- Reporte de gastos ----------------------------------------------------
	// Cajero NO accede al reporte => 403.
	if resp := env.do(t, cashier, http.MethodGet, "/reports/expenses", nil); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cajero GET /reports/expenses: esperaba 403, obtuvo %d", resp.StatusCode)
	}
	// Super admin sí, con rango amplio.
	from := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)
	to := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	resp = env.do(t, root, http.MethodGet, "/reports/expenses?from="+from+"&to="+to, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("super admin GET /reports/expenses: esperaba 200, obtuvo %d", resp.StatusCode)
	}
	var rep struct {
		Summary struct {
			ExpensesCount int `json:"expensesCount"`
			TotalCents    int `json:"totalCents"`
		} `json:"summary"`
		ByCategory []struct {
			CategoryName string `json:"categoryName"`
		} `json:"byCategory"`
	}
	decode(t, resp, &rep)
	// Tras los borrados no quedan gastos; el reporte responde con estructura vacía.
	if rep.Summary.ExpensesCount != 0 || rep.Summary.TotalCents != 0 {
		t.Fatalf("reporte tras borrados: esperaba vacío, obtuvo %+v", rep.Summary)
	}
	if rep.ByCategory == nil {
		t.Fatal("byCategory no debe ser nil (JSON [] esperado)")
	}
}
