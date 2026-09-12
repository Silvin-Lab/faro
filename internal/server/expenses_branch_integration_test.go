package server_test

import (
	"net/http"
	"testing"

	"faro/internal/expenses"
)

// createExpenseConcept crea (como super admin) una categoría + concepto de gasto vía
// HTTP y devuelve el id del concepto activo, listo para registrar gastos.
func (e *m7Env) createExpenseConcept(t *testing.T, c *http.Client, catName, conceptName string) string {
	t.Helper()
	resp := e.do(t, c, http.MethodPost, "/expenses/categories", map[string]any{"name": catName, "sortOrder": 0})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /expenses/categories: esperaba 201, obtuvo %d", resp.StatusCode)
	}
	var cat struct {
		Category expenses.Category `json:"category"`
	}
	decode(t, resp, &cat)

	resp = e.do(t, c, http.MethodPost, "/expenses/concepts", map[string]any{"name": conceptName, "categoryId": cat.Category.ID})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /expenses/concepts: esperaba 201, obtuvo %d", resp.StatusCode)
	}
	var con struct {
		Concept expenses.Concept `json:"concept"`
	}
	decode(t, resp, &con)
	return con.Concept.ID
}

// TestExpensesBranchOptional cubre la feature 0021: la administración central
// (super_admin) puede registrar gastos de una sucursal específica o "General" (sin
// sucursal), mientras que el personal de sucursal sigue atado a su sucursal activa.
func TestExpensesBranchOptional(t *testing.T) {
	env := setupM7(t)
	defer env.close()

	root := newClient(t)
	env.login(t, root, "root@faro.test", "secret123")

	b1 := env.createBranch(t, root, "Centro")
	b2 := env.createBranch(t, root, "Norte")
	conceptID := env.createExpenseConcept(t, root, "Servicios", "Luz")

	type expenseResp struct {
		Expense expenses.Expense `json:"expense"`
	}
	type errResp struct {
		Code string `json:"code"`
	}

	// 1) super_admin crea un gasto CON branchId de una sucursal válida => 201 con esa
	//    sucursal.
	resp := env.do(t, root, http.MethodPost, "/expenses/", map[string]any{
		"conceptId": conceptID, "amountCents": 15000, "branchId": b1,
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("super_admin gasto con branchId válido: esperaba 201, obtuvo %d", resp.StatusCode)
	}
	var withBranch expenseResp
	decode(t, resp, &withBranch)
	if withBranch.Expense.BranchID == nil || *withBranch.Expense.BranchID != b1 {
		t.Fatalf("gasto debía quedar en b1, obtuvo %+v", withBranch.Expense.BranchID)
	}
	if withBranch.Expense.BranchName == nil || *withBranch.Expense.BranchName != "Centro" {
		t.Fatalf("gasto debía traer branchName 'Centro', obtuvo %+v", withBranch.Expense.BranchName)
	}

	// 2) super_admin crea un gasto SIN branchId => 201 con branchId null ("General").
	resp = env.do(t, root, http.MethodPost, "/expenses/", map[string]any{
		"conceptId": conceptID, "amountCents": 20000,
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("super_admin gasto General (sin branchId): esperaba 201, obtuvo %d", resp.StatusCode)
	}
	var general expenseResp
	decode(t, resp, &general)
	if general.Expense.BranchID != nil {
		t.Fatalf("gasto General debía tener branchId null, obtuvo %+v", *general.Expense.BranchID)
	}
	if general.Expense.BranchName != nil {
		t.Fatalf("gasto General debía tener branchName null, obtuvo %+v", *general.Expense.BranchName)
	}

	// 3) super_admin crea un gasto con branchId inexistente/ajeno => 400 invalid_branch.
	resp = env.do(t, root, http.MethodPost, "/expenses/", map[string]any{
		"conceptId": conceptID, "amountCents": 5000, "branchId": "00000000-0000-0000-0000-000000000000",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("super_admin gasto con branchId inexistente: esperaba 400, obtuvo %d", resp.StatusCode)
	}
	var e3 errResp
	decode(t, resp, &e3)
	if e3.Code != "invalid_branch" {
		t.Fatalf("gasto con branchId inexistente: esperaba code invalid_branch, obtuvo %q", e3.Code)
	}
	// uuid mal formado => también invalid_branch (no 500).
	resp = env.do(t, root, http.MethodPost, "/expenses/", map[string]any{
		"conceptId": conceptID, "amountCents": 5000, "branchId": "no-es-uuid",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("super_admin gasto con branchId mal formado: esperaba 400, obtuvo %d", resp.StatusCode)
	}

	// 4) El personal de sucursal ignora el branchId del body: queda su sucursal activa.
	if resp := env.do(t, root, http.MethodPost, "/users", map[string]any{
		"email": "cajero-gastos@vanta.test", "password": "secret123", "name": "Cajero",
		"role": "cashier", "branchIds": []string{b1},
	}); resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /users cajero: esperaba 201, obtuvo %d", resp.StatusCode)
	}
	cajero := newClient(t)
	env.login(t, cajero, "cajero-gastos@vanta.test", "secret123")
	env.do(t, cajero, http.MethodPost, "/auth/select-branch", map[string]string{"branchId": b1}).Body.Close()

	// El cajero (activo en b1) intenta inyectar b2 en el body => se ignora, queda b1.
	resp = env.do(t, cajero, http.MethodPost, "/expenses/", map[string]any{
		"conceptId": conceptID, "amountCents": 3000, "branchId": b2,
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("cajero gasto: esperaba 201, obtuvo %d", resp.StatusCode)
	}
	var byCashier expenseResp
	decode(t, resp, &byCashier)
	if byCashier.Expense.BranchID == nil || *byCashier.Expense.BranchID != b1 {
		t.Fatalf("gasto del cajero debía quedar en su sucursal activa b1 (no b2 del body), obtuvo %+v", byCashier.Expense.BranchID)
	}

	// El cajero NO puede borrar el gasto "General" (branchId null): 403 (solo super_admin).
	if resp := env.do(t, cajero, http.MethodDelete, "/expenses/"+general.Expense.ID, nil); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cajero DELETE gasto General: esperaba 403, obtuvo %d", resp.StatusCode)
	}
	// El super_admin sí puede borrar el gasto "General".
	if resp := env.do(t, root, http.MethodDelete, "/expenses/"+general.Expense.ID, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("super_admin DELETE gasto General: esperaba 200, obtuvo %d", resp.StatusCode)
	}
}
