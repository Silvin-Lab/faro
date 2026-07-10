package server_test

import (
	"net/http"
	"testing"
)

// createSupply crea un insumo vía HTTP como super admin y devuelve su id.
func (e *m7Env) createSupply(t *testing.T, c *http.Client, name, baseUnit, pkgName string, pkgContent int) string {
	t.Helper()
	resp := e.do(t, c, http.MethodPost, "/supplies", map[string]any{
		"name": name, "baseUnit": baseUnit, "packageName": pkgName, "packageContent": pkgContent,
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /supplies %s: esperaba 201, obtuvo %d", name, resp.StatusCode)
	}
	var body struct {
		Supply struct {
			ID string `json:"id"`
		} `json:"supply"`
	}
	decode(t, resp, &body)
	return body.Supply.ID
}

// TestSuppliesHTTP cubre el flujo HTTP de insumos: authz (cajero 403 en escritura,
// lectura OK), movimiento de compra que devuelve el stock, branchId ajeno rechazado
// y authz de recetas.
func TestSuppliesHTTP(t *testing.T) {
	env := setupM7(t)
	defer env.close()

	root := newClient(t)
	env.login(t, root, "root@faro.test", "secret123")

	b1 := env.createBranch(t, root, "Centro")
	b2 := env.createBranch(t, root, "Norte")

	// Super admin crea un insumo (Bote 900 ml).
	supplyID := env.createSupply(t, root, "Leche", "ml", "Bote 900 ml", 900)

	// Usuario cajero (miembro de b1 y b2).
	env.newBranchUser(t, root, "cajero@vanta.test", "cashier", []string{b1, b2})
	cashier := newClient(t)
	env.login(t, cashier, "cajero@vanta.test", "secret123")

	// --- Authz de escritura del catálogo: cajero 403 --------------------------
	if resp := env.do(t, cashier, http.MethodPost, "/supplies", map[string]any{
		"name": "Hack", "baseUnit": "g", "packageName": "x", "packageContent": 1,
	}); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cajero POST /supplies: esperaba 403, obtuvo %d", resp.StatusCode)
	}
	// Lectura del catálogo SÍ permitida.
	if resp := env.do(t, cashier, http.MethodGet, "/supplies", nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("cajero GET /supplies: esperaba 200, obtuvo %d", resp.StatusCode)
	}
	// Movimientos: cajero 403.
	if resp := env.do(t, cashier, http.MethodPost, "/supplies/"+supplyID+"/movements", map[string]any{
		"type": "purchase", "branchId": b1, "packages": 1,
	}); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cajero POST movements: esperaba 403, obtuvo %d", resp.StatusCode)
	}
	// Recetas (PUT): cajero 403.
	if resp := env.do(t, cashier, http.MethodPut, "/supplies/recipes/"+env.productA, map[string]any{
		"items": []map[string]any{{"supplyId": supplyID, "quantityBase": 100}},
	}); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cajero PUT recetas: esperaba 403, obtuvo %d", resp.StatusCode)
	}

	// --- Super admin: compra 2 x 900 en b1 => stock 1800 ----------------------
	resp := env.do(t, root, http.MethodPost, "/supplies/"+supplyID+"/movements", map[string]any{
		"type": "purchase", "branchId": b1, "packages": 2,
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("compra: esperaba 201, obtuvo %d", resp.StatusCode)
	}
	var mv struct {
		Movement struct {
			QuantityBase int `json:"quantityBase"`
		} `json:"movement"`
		StockBase int `json:"stockBase"`
	}
	decode(t, resp, &mv)
	if mv.Movement.QuantityBase != 1800 || mv.StockBase != 1800 {
		t.Fatalf("compra 2x900: esperaba qty=1800 stock=1800, obtuvo %+v", mv)
	}

	// --- branchId que no pertenece al negocio => rechazado (invalid_branch) ---
	// No se puede sembrar un segundo negocio (rompería la resolución del negocio
	// único del super admin), así que se usa un uuid de sucursal inexistente: recorre
	// exactamente la misma validación branchInTenant que una sucursal de otro tenant.
	// El caso cross-tenant real está cubierto en el test de servicio (branchB1).
	resp = env.do(t, root, http.MethodPost, "/supplies/"+supplyID+"/movements", map[string]any{
		"type": "purchase", "branchId": "00000000-0000-0000-0000-000000000000", "packages": 1,
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("branch ajeno: esperaba 400, obtuvo %d", resp.StatusCode)
	}
	var perr struct {
		Code string `json:"code"`
	}
	decode(t, resp, &perr)
	if perr.Code != "invalid_branch" {
		t.Fatalf("branch ajeno: esperaba code invalid_branch, obtuvo %q", perr.Code)
	}

	// --- base_unit inmutable vía PATCH => validation_error --------------------
	resp = env.do(t, root, http.MethodPatch, "/supplies/"+supplyID, map[string]any{"baseUnit": "g"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("PATCH baseUnit: esperaba 400, obtuvo %d", resp.StatusCode)
	}
	decode(t, resp, &perr)
	if perr.Code != "validation_error" {
		t.Fatalf("PATCH baseUnit: esperaba validation_error, obtuvo %q", perr.Code)
	}

	// --- Super admin: receta replace-all OK -----------------------------------
	resp = env.do(t, root, http.MethodPut, "/supplies/recipes/"+env.productA, map[string]any{
		"items": []map[string]any{{"supplyId": supplyID, "quantityBase": 200}},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT receta: esperaba 200, obtuvo %d", resp.StatusCode)
	}
	var rec struct {
		Items []struct {
			SupplyID     string `json:"supplyId"`
			QuantityBase int    `json:"quantityBase"`
		} `json:"items"`
	}
	decode(t, resp, &rec)
	if len(rec.Items) != 1 || rec.Items[0].QuantityBase != 200 {
		t.Fatalf("receta inesperada: %+v", rec.Items)
	}

	// El cajero SÍ puede leer la receta (GET por sesión).
	if resp := env.do(t, cashier, http.MethodGet, "/supplies/recipes/"+env.productA, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("cajero GET receta: esperaba 200, obtuvo %d", resp.StatusCode)
	}

	_ = b2
}

// TestSupplyCategoriesHTTP cubre el sub-recurso /supplies/categories: authz (cajero
// 403 en escritura, lectura OK), que la respuesta de POST devuelve la categoría
// completa (creación inline en el frontend), el ruteo estático (GET
// /supplies/categories NO cae en handleGet y GET /supplies/{uuid} sigue funcionando)
// y crear un insumo con categoryId (categoryName en la respuesta).
func TestSupplyCategoriesHTTP(t *testing.T) {
	env := setupM7(t)
	defer env.close()

	root := newClient(t)
	env.login(t, root, "root@faro.test", "secret123")

	b1 := env.createBranch(t, root, "Centro")
	env.newBranchUser(t, root, "cajero@vanta.test", "cashier", []string{b1})
	cashier := newClient(t)
	env.login(t, cashier, "cajero@vanta.test", "secret123")

	// --- Super admin crea una categoría: 201 con la categoría COMPLETA -----------
	resp := env.do(t, root, http.MethodPost, "/supplies/categories", map[string]any{
		"name": "Lácteos", "sortOrder": 2,
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /supplies/categories: esperaba 201, obtuvo %d", resp.StatusCode)
	}
	var cat struct {
		Category struct {
			ID        string `json:"id"`
			Name      string `json:"name"`
			Status    string `json:"status"`
			SortOrder int    `json:"sortOrder"`
		} `json:"category"`
	}
	decode(t, resp, &cat)
	if cat.Category.ID == "" || cat.Category.Name != "Lácteos" || cat.Category.Status != "active" || cat.Category.SortOrder != 2 {
		t.Fatalf("categoría creada incompleta: %+v", cat.Category)
	}

	// name_taken -> 409.
	if resp := env.do(t, root, http.MethodPost, "/supplies/categories", map[string]any{"name": "Lácteos"}); resp.StatusCode != http.StatusConflict {
		t.Fatalf("categoría duplicada: esperaba 409, obtuvo %d", resp.StatusCode)
	}

	// --- Authz: cajero 403 en POST/PATCH categorías -----------------------------
	if resp := env.do(t, cashier, http.MethodPost, "/supplies/categories", map[string]any{"name": "Hack"}); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cajero POST categorías: esperaba 403, obtuvo %d", resp.StatusCode)
	}
	if resp := env.do(t, cashier, http.MethodPatch, "/supplies/categories/"+cat.Category.ID, map[string]any{"name": "Hack"}); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cajero PATCH categorías: esperaba 403, obtuvo %d", resp.StatusCode)
	}

	// --- Ruteo estático: GET /supplies/categories NO cae en handleGet -----------
	// (handleGet devolvería 404 not_found por un uuid inválido "categories").
	resp = env.do(t, cashier, http.MethodGet, "/supplies/categories", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /supplies/categories: esperaba 200, obtuvo %d", resp.StatusCode)
	}
	var list struct {
		Items []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"items"`
	}
	decode(t, resp, &list)
	if len(list.Items) != 1 || list.Items[0].Name != "Lácteos" {
		t.Fatalf("GET /supplies/categories: esperaba [Lácteos], obtuvo %+v", list.Items)
	}

	// --- PATCH categoría: renombra + reordena -----------------------------------
	resp = env.do(t, root, http.MethodPatch, "/supplies/categories/"+cat.Category.ID, map[string]any{
		"name": "Lácteos y quesos", "sortOrder": 5,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PATCH categoría: esperaba 200, obtuvo %d", resp.StatusCode)
	}
	decode(t, resp, &cat)
	if cat.Category.Name != "Lácteos y quesos" || cat.Category.SortOrder != 5 {
		t.Fatalf("PATCH categoría inesperado: %+v", cat.Category)
	}
	// PATCH categoría inexistente -> 404.
	if resp := env.do(t, root, http.MethodPatch, "/supplies/categories/00000000-0000-0000-0000-000000000000", map[string]any{"name": "X"}); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("PATCH categoría inexistente: esperaba 404, obtuvo %d", resp.StatusCode)
	}

	// --- Crear insumo CON categoría: categoryId/categoryName en la respuesta -----
	resp = env.do(t, root, http.MethodPost, "/supplies", map[string]any{
		"name": "Leche", "baseUnit": "ml", "packageName": "Bote 900 ml", "packageContent": 900,
		"categoryId": cat.Category.ID,
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /supplies con categoría: esperaba 201, obtuvo %d", resp.StatusCode)
	}
	var sup struct {
		Supply struct {
			ID           string  `json:"id"`
			CategoryID   *string `json:"categoryId"`
			CategoryName *string `json:"categoryName"`
		} `json:"supply"`
	}
	decode(t, resp, &sup)
	if sup.Supply.CategoryID == nil || *sup.Supply.CategoryID != cat.Category.ID {
		t.Fatalf("insumo con categoría: categoryId inesperado %+v", sup.Supply.CategoryID)
	}
	if sup.Supply.CategoryName == nil || *sup.Supply.CategoryName != "Lácteos y quesos" {
		t.Fatalf("insumo con categoría: categoryName esperaba 'Lácteos y quesos', obtuvo %v", sup.Supply.CategoryName)
	}

	// --- El ruteo /{id} sigue vivo: GET /supplies/{uuid} devuelve el insumo ------
	resp = env.do(t, cashier, http.MethodGet, "/supplies/"+sup.Supply.ID, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /supplies/{uuid}: esperaba 200, obtuvo %d", resp.StatusCode)
	}
	decode(t, resp, &sup)
	if sup.Supply.CategoryName == nil || *sup.Supply.CategoryName != "Lácteos y quesos" {
		t.Fatalf("GET /supplies/{uuid}: categoryName esperaba 'Lácteos y quesos', obtuvo %v", sup.Supply.CategoryName)
	}

	// --- Categoría ajena/inexistente en create -> 400 invalid_category ----------
	resp = env.do(t, root, http.MethodPost, "/supplies", map[string]any{
		"name": "Café", "baseUnit": "g", "packageName": "Bolsa", "packageContent": 1000,
		"categoryId": "00000000-0000-0000-0000-000000000000",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("categoría inválida en create: esperaba 400, obtuvo %d", resp.StatusCode)
	}
	var perr struct {
		Code string `json:"code"`
	}
	decode(t, resp, &perr)
	if perr.Code != "invalid_category" {
		t.Fatalf("categoría inválida: esperaba code invalid_category, obtuvo %q", perr.Code)
	}
}
