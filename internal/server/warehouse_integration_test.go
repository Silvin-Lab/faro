package server_test

import (
	"net/http"
	"testing"
	"time"
)

// createWarehouseSupply crea un insumo vía HTTP como super admin y devuelve su id.
func (e *m7Env) createWarehouseSupply(t *testing.T, c *http.Client, name, baseUnit, pkgName string, pkgContent int) string {
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

// TestWarehouseGating verifica que TODAS las rutas de /warehouse (lectura y
// escritura) exigen super_admin: un cajero recibe 403 en cada una.
func TestWarehouseGating(t *testing.T) {
	env := setupM7(t)
	defer env.close()

	root := newClient(t)
	env.login(t, root, "root@faro.test", "secret123")
	b1 := env.createBranch(t, root, "Centro")
	env.newBranchUser(t, root, "cajero@vanta.test", "cashier", []string{b1})

	cashier := newClient(t)
	env.login(t, cashier, "cajero@vanta.test", "secret123")

	type route struct {
		method, path string
		body         any
	}
	routes := []route{
		{http.MethodGet, "/warehouse/stock", nil},
		{http.MethodGet, "/warehouse/to-buy", nil},
		{http.MethodGet, "/warehouse/suppliers", nil},
		{http.MethodPost, "/warehouse/suppliers", map[string]any{"name": "X"}},
		{http.MethodPatch, "/warehouse/suppliers/00000000-0000-0000-0000-000000000000", map[string]any{"name": "X"}},
		{http.MethodPatch, "/warehouse/stock/00000000-0000-0000-0000-000000000000", map[string]any{"minQuantity": 1}},
		{http.MethodPost, "/warehouse/purchases", map[string]any{"supplyId": "x", "supplierId": "y", "packages": 1, "unitCostCents": 1}},
		{http.MethodGet, "/warehouse/purchases", nil},
		{http.MethodPost, "/warehouse/dispatches", map[string]any{"supplyId": "x", "branchId": "y", "quantityBase": 1}},
		{http.MethodGet, "/warehouse/dispatches", nil},
		{http.MethodPost, "/warehouse/waste", map[string]any{"supplyId": "x", "quantityBase": 1, "reason": "r"}},
		{http.MethodGet, "/warehouse/waste", nil},
	}
	for _, r := range routes {
		resp := env.do(t, cashier, r.method, r.path, r.body)
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("cajero %s %s: esperaba 403, obtuvo %d", r.method, r.path, resp.StatusCode)
		}
	}
}

// TestWarehouseFlowHTTP cubre el flujo end-to-end por HTTP: proveedor, compra,
// salida (con el transfer visible en el historial de la sucursal — F9+F10), la
// regresión 100/10 galletas y el historial de mermas UNION.
func TestWarehouseFlowHTTP(t *testing.T) {
	env := setupM7(t)
	defer env.close()

	root := newClient(t)
	env.login(t, root, "root@faro.test", "secret123")
	b1 := env.createBranch(t, root, "Centro")
	supplyID := env.createWarehouseSupply(t, root, "Galletas", "pieza", "Caja 10", 10)

	// --- Proveedor: alta + name_taken -------------------------------------
	resp := env.do(t, root, http.MethodPost, "/warehouse/suppliers", map[string]any{"name": "Distribuidora"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST supplier: esperaba 201, obtuvo %d", resp.StatusCode)
	}
	var sup struct {
		Supplier struct {
			ID string `json:"id"`
		} `json:"supplier"`
	}
	decode(t, resp, &sup)
	if resp := env.do(t, root, http.MethodPost, "/warehouse/suppliers", map[string]any{"name": "Distribuidora"}); resp.StatusCode != http.StatusConflict {
		t.Fatalf("supplier duplicado: esperaba 409, obtuvo %d", resp.StatusCode)
	}

	// --- Compra: 20 cajas x 10 = 200 en el almacén ------------------------
	resp = env.do(t, root, http.MethodPost, "/warehouse/purchases", map[string]any{
		"supplyId": supplyID, "supplierId": sup.Supplier.ID, "packages": 20, "unitCostCents": 300,
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST purchase: esperaba 201, obtuvo %d", resp.StatusCode)
	}
	var pur struct {
		Movement struct {
			QuantityBase int `json:"quantityBase"`
		} `json:"movement"`
		StockBase int `json:"stockBase"`
	}
	decode(t, resp, &pur)
	if pur.Movement.QuantityBase != 200 || pur.StockBase != 200 {
		t.Fatalf("compra 20x10: esperaba qty=200 stock=200, obtuvo %+v", pur)
	}

	// --- Salida: 100 a b1 -> almacén 100, sucursal 100 --------------------
	resp = env.do(t, root, http.MethodPost, "/warehouse/dispatches", map[string]any{
		"supplyId": supplyID, "branchId": b1, "quantityBase": 100,
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST dispatch: esperaba 201, obtuvo %d", resp.StatusCode)
	}
	var disp struct {
		WarehouseStockBase int `json:"warehouseStockBase"`
		BranchStockBase    int `json:"branchStockBase"`
	}
	decode(t, resp, &disp)
	if disp.WarehouseStockBase != 100 || disp.BranchStockBase != 100 {
		t.Fatalf("dispatch 100: esperaba wh=100 branch=100, obtuvo %+v", disp)
	}

	// --- F9+F10: el transfer aparece en el historial de la sucursal con fecha ---
	resp = env.do(t, root, http.MethodGet, "/supplies/"+supplyID+"/movements", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET movements: esperaba 200, obtuvo %d", resp.StatusCode)
	}
	var mov struct {
		Items []struct {
			Type         string    `json:"type"`
			QuantityBase int       `json:"quantityBase"`
			BranchID     *string   `json:"branchId"`
			CreatedAt    time.Time `json:"createdAt"`
		} `json:"items"`
	}
	decode(t, resp, &mov)
	var foundTransfer bool
	for _, it := range mov.Items {
		if it.Type == "transfer" {
			foundTransfer = true
			if it.QuantityBase != 100 || it.BranchID == nil || *it.BranchID != b1 || it.CreatedAt.IsZero() {
				t.Fatalf("transfer en historial sucursal inesperado: %+v", it)
			}
		}
	}
	if !foundTransfer {
		t.Fatalf("F10: el transfer debía aparecer en el historial de la sucursal")
	}

	// --- Regresión 100/10 galletas: waste branch=b1 de 10 -----------------
	resp = env.do(t, root, http.MethodPost, "/warehouse/waste", map[string]any{
		"supplyId": supplyID, "quantityBase": 10, "reason": "se cayeron", "branchId": b1,
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST waste branch: esperaba 201, obtuvo %d", resp.StatusCode)
	}
	var wst struct {
		Movement struct {
			Origin string `json:"origin"`
		} `json:"movement"`
		StockBase int `json:"stockBase"`
	}
	decode(t, resp, &wst)
	if wst.Movement.Origin != "branch" || wst.StockBase != 90 {
		t.Fatalf("waste branch: esperaba origin=branch stock=90, obtuvo %+v", wst)
	}

	// El almacén sigue en 100 (SIN déficit fantasma): GET /warehouse/stock.
	resp = env.do(t, root, http.MethodGet, "/warehouse/stock", nil)
	var stock struct {
		Items []struct {
			SupplyID  string `json:"supplyId"`
			StockBase int    `json:"stockBase"`
		} `json:"items"`
	}
	decode(t, resp, &stock)
	var whStock int
	for _, it := range stock.Items {
		if it.SupplyID == supplyID {
			whStock = it.StockBase
		}
	}
	if whStock != 100 {
		t.Fatalf("REGRESIÓN HTTP: almacén debía seguir en 100 (sin déficit fantasma), obtuvo %d", whStock)
	}

	// --- Merma sin sucursal de 5 -> almacén 95 ----------------------------
	resp = env.do(t, root, http.MethodPost, "/warehouse/waste", map[string]any{
		"supplyId": supplyID, "quantityBase": 5, "reason": "caducó",
	})
	decode(t, resp, &wst)
	if wst.Movement.Origin != "warehouse" || wst.StockBase != 95 {
		t.Fatalf("waste almacén: esperaba origin=warehouse stock=95, obtuvo %+v", wst)
	}

	// --- Historial de mermas UNION: 2 filas (branch + warehouse) ----------
	resp = env.do(t, root, http.MethodGet, "/warehouse/waste", nil)
	var wasteHist struct {
		Items []struct {
			Origin       string `json:"origin"`
			QuantityBase int    `json:"quantityBase"`
		} `json:"items"`
	}
	decode(t, resp, &wasteHist)
	if len(wasteHist.Items) != 2 {
		t.Fatalf("historial mermas UNION: esperaba 2, obtuvo %d", len(wasteHist.Items))
	}
}

// TestSuppliesManualMovementRejectsWarehouseTypes verifica B9/ADR-008 D4: la API
// manual de sucursal NO acepta transfer/waste/sale (solo purchase/adjustment).
func TestSuppliesManualMovementRejectsWarehouseTypes(t *testing.T) {
	env := setupM7(t)
	defer env.close()

	root := newClient(t)
	env.login(t, root, "root@faro.test", "secret123")
	b1 := env.createBranch(t, root, "Centro")
	supplyID := env.createWarehouseSupply(t, root, "Leche", "ml", "Bote 900", 900)

	for _, badType := range []string{"transfer", "waste", "sale"} {
		resp := env.do(t, root, http.MethodPost, "/supplies/"+supplyID+"/movements", map[string]any{
			"type": badType, "branchId": b1, "quantityBase": 100, "reason": "x",
		})
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("POST movement type=%s: esperaba 400, obtuvo %d", badType, resp.StatusCode)
		}
		var perr struct {
			Code string `json:"code"`
		}
		decode(t, resp, &perr)
		if perr.Code != "validation_error" {
			t.Fatalf("POST movement type=%s: esperaba validation_error, obtuvo %q", badType, perr.Code)
		}
	}
}
