package server_test

import (
	"context"
	"net/http"
	"testing"
)

// reqDetail refleja la respuesta de detalle/creación de una requisición.
type reqDetail struct {
	Requisition struct {
		ID       string `json:"id"`
		BranchID string `json:"branchId"`
		Status   string `json:"status"`
	} `json:"requisition"`
	Items []struct {
		ID                string `json:"id"`
		SupplyID          string `json:"supplyId"`
		QuantityRequested int    `json:"quantityRequested"`
		QuantityFulfilled int    `json:"quantityFulfilled"`
	} `json:"items"`
	Movements []struct {
		ID string `json:"id"`
	} `json:"movements"`
}

// TestRequisitionLifecycle: crear -> dispatch parcial -> dispatch que completa -> fulfilled,
// con la traza de movimientos ligada.
func TestRequisitionLifecycle(t *testing.T) {
	env := setupM7(t)
	defer env.close()

	root := newClient(t)
	env.login(t, root, "root@faro.test", "secret123")
	b1 := env.createBranch(t, root, "Centro")
	harina := env.createWarehouseSupply(t, root, "Harina", "g", "Bolsa 1000", 1)
	azucar := env.createWarehouseSupply(t, root, "Azúcar", "g", "Bolsa 1000", 1)
	env.adjustWarehouse(t, root, harina, 100000)
	env.adjustWarehouse(t, root, azucar, 100000)

	env.newBranchUser(t, root, "cajero@vanta.test", "cashier", []string{b1})
	cajero := newClient(t)
	env.login(t, cajero, "cajero@vanta.test", "secret123")

	// Sucursal crea la requisición (2 líneas).
	resp := env.do(t, cajero, http.MethodPost, "/requisitions/supplies", map[string]any{
		"note": "faltantes del día",
		"items": []map[string]any{
			{"supplyId": harina, "quantityBase": 100},
			{"supplyId": azucar, "quantityBase": 50},
		},
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("crear requisición: esperaba 201, obtuvo %d", resp.StatusCode)
	}
	var d reqDetail
	decode(t, resp, &d)
	if d.Requisition.Status != "pending" || len(d.Items) != 2 {
		t.Fatalf("requisición creada inesperada: %+v", d)
	}
	reqID := d.Requisition.ID
	var itemHarina, itemAzucar string
	for _, it := range d.Items {
		if it.SupplyID == harina {
			itemHarina = it.ID
		}
		if it.SupplyID == azucar {
			itemAzucar = it.ID
		}
	}

	// Super admin surte parcialmente la harina (60 de 100) vía dispatch ligado.
	resp = env.do(t, root, http.MethodPost, "/warehouse/dispatches", map[string]any{
		"supplyId": harina, "branchId": b1, "quantityBase": 60, "requisitionItemId": itemHarina,
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("dispatch parcial: esperaba 201, obtuvo %d", resp.StatusCode)
	}
	resp.Body.Close()

	resp = env.do(t, cajero, http.MethodGet, "/requisitions/supplies/"+reqID, nil)
	decode(t, resp, &d)
	if d.Requisition.Status != "partial" {
		t.Fatalf("tras dispatch parcial: esperaba partial, obtuvo %s", d.Requisition.Status)
	}
	if len(d.Movements) != 1 {
		t.Fatalf("traza de movimientos: esperaba 1, obtuvo %d", len(d.Movements))
	}

	// Completar harina (40 más) y azúcar (50): fulfilled.
	env.do(t, root, http.MethodPost, "/warehouse/dispatches", map[string]any{
		"supplyId": harina, "branchId": b1, "quantityBase": 40, "requisitionItemId": itemHarina,
	}).Body.Close()
	env.do(t, root, http.MethodPost, "/warehouse/dispatches", map[string]any{
		"supplyId": azucar, "branchId": b1, "quantityBase": 50, "requisitionItemId": itemAzucar,
	}).Body.Close()

	resp = env.do(t, cajero, http.MethodGet, "/requisitions/supplies/"+reqID, nil)
	decode(t, resp, &d)
	if d.Requisition.Status != "fulfilled" {
		t.Fatalf("tras completar: esperaba fulfilled, obtuvo %s", d.Requisition.Status)
	}
	for _, it := range d.Items {
		if it.QuantityFulfilled != it.QuantityRequested {
			t.Fatalf("línea %s no completa: %d/%d", it.SupplyID, it.QuantityFulfilled, it.QuantityRequested)
		}
	}

	// Mismatch de insumo: dispatch de azúcar contra el item de harina => 400.
	if resp := env.do(t, root, http.MethodPost, "/warehouse/dispatches", map[string]any{
		"supplyId": azucar, "branchId": b1, "quantityBase": 1, "requisitionItemId": itemHarina,
	}); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("dispatch mismatch: esperaba 400, obtuvo %d", resp.StatusCode)
	}
}

// TestRequisitionCancelBlockedWhenFulfilled: cancelar solo si pending sin surtir.
func TestRequisitionCancelBlockedWhenFulfilled(t *testing.T) {
	env := setupM7(t)
	defer env.close()

	root := newClient(t)
	env.login(t, root, "root@faro.test", "secret123")
	b1 := env.createBranch(t, root, "Centro")
	harina := env.createWarehouseSupply(t, root, "Harina", "g", "Bolsa 1000", 1)
	env.adjustWarehouse(t, root, harina, 100000)
	env.newBranchUser(t, root, "cajero@vanta.test", "cashier", []string{b1})
	cajero := newClient(t)
	env.login(t, cajero, "cajero@vanta.test", "secret123")

	// Requisición cancelable (pending) => 200.
	resp := env.do(t, cajero, http.MethodPost, "/requisitions/supplies", map[string]any{
		"items": []map[string]any{{"supplyId": harina, "quantityBase": 10}},
	})
	var d1 reqDetail
	decode(t, resp, &d1)
	if resp := env.do(t, cajero, http.MethodPatch, "/requisitions/supplies/"+d1.Requisition.ID+"/cancel", nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("cancel pending: esperaba 200, obtuvo %d", resp.StatusCode)
	}

	// Requisición con algo surtido => NO cancelable (409).
	resp = env.do(t, cajero, http.MethodPost, "/requisitions/supplies", map[string]any{
		"items": []map[string]any{{"supplyId": harina, "quantityBase": 10}},
	})
	var d2 reqDetail
	decode(t, resp, &d2)
	env.do(t, root, http.MethodPost, "/warehouse/dispatches", map[string]any{
		"supplyId": harina, "branchId": b1, "quantityBase": 5, "requisitionItemId": d2.Items[0].ID,
	}).Body.Close()
	if resp := env.do(t, cajero, http.MethodPatch, "/requisitions/supplies/"+d2.Requisition.ID+"/cancel", nil); resp.StatusCode != http.StatusConflict {
		t.Fatalf("cancel con surtido: esperaba 409, obtuvo %d", resp.StatusCode)
	}

	// close (super admin) fuerza partial -> fulfilled.
	if resp := env.do(t, root, http.MethodPatch, "/requisitions/supplies/"+d2.Requisition.ID+"/close", nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("close partial: esperaba 200, obtuvo %d", resp.StatusCode)
	}
	// La sucursal NO puede cerrar (403).
	resp = env.do(t, cajero, http.MethodPost, "/requisitions/supplies", map[string]any{
		"items": []map[string]any{{"supplyId": harina, "quantityBase": 10}},
	})
	var d3 reqDetail
	decode(t, resp, &d3)
	if resp := env.do(t, cajero, http.MethodPatch, "/requisitions/supplies/"+d3.Requisition.ID+"/close", nil); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("sucursal close: esperaba 403, obtuvo %d", resp.StatusCode)
	}
}

// TestRequisitionCrossBranchAccess: un cajero de otra sucursal no puede ver ni cancelar la
// requisición de la sucursal ajena (403 por id), aunque conozca el uuid.
func TestRequisitionCrossBranchAccess(t *testing.T) {
	env := setupM7(t)
	defer env.close()

	root := newClient(t)
	env.login(t, root, "root@faro.test", "secret123")
	b1 := env.createBranch(t, root, "Centro")
	b2 := env.createBranch(t, root, "Norte")
	harina := env.createWarehouseSupply(t, root, "Harina", "g", "Bolsa 1000", 1)

	env.newBranchUser(t, root, "c1@vanta.test", "cashier", []string{b1})
	env.newBranchUser(t, root, "c2@vanta.test", "cashier", []string{b2})
	c1 := newClient(t)
	env.login(t, c1, "c1@vanta.test", "secret123")
	c2 := newClient(t)
	env.login(t, c2, "c2@vanta.test", "secret123")

	// c1 (b1) crea una requisición.
	resp := env.do(t, c1, http.MethodPost, "/requisitions/supplies", map[string]any{
		"items": []map[string]any{{"supplyId": harina, "quantityBase": 10}},
	})
	var d reqDetail
	decode(t, resp, &d)
	id := d.Requisition.ID

	// c2 (b2) NO puede ver la requisición ajena por id (403).
	if resp := env.do(t, c2, http.MethodGet, "/requisitions/supplies/"+id, nil); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("c2 GET requisición ajena: esperaba 403, obtuvo %d", resp.StatusCode)
	}
	// c2 (b2) NO puede cancelar la requisición ajena (403).
	if resp := env.do(t, c2, http.MethodPatch, "/requisitions/supplies/"+id+"/cancel", nil); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("c2 PATCH cancel requisición ajena: esperaba 403, obtuvo %d", resp.StatusCode)
	}
	// El dueño (c1) sí puede verla.
	if resp := env.do(t, c1, http.MethodGet, "/requisitions/supplies/"+id, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("c1 GET propia: esperaba 200, obtuvo %d", resp.StatusCode)
	}
}

// TestRequisitionGatingAndScope: sucursal solo ve/crea la suya; super admin ve todas;
// repostero => 403; suggestions solo devuelve lo que está bajo mínimo.
func TestRequisitionGatingAndScope(t *testing.T) {
	env := setupM7(t)
	defer env.close()
	ctx := context.Background()

	root := newClient(t)
	env.login(t, root, "root@faro.test", "secret123")
	b1 := env.createBranch(t, root, "Centro")
	b2 := env.createBranch(t, root, "Norte")
	harina := env.createWarehouseSupply(t, root, "Harina", "g", "Bolsa 1000", 1)
	azucar := env.createWarehouseSupply(t, root, "Azúcar", "g", "Bolsa 1000", 1)

	env.newBranchUser(t, root, "c1@vanta.test", "cashier", []string{b1})
	env.newBranchUser(t, root, "c2@vanta.test", "cashier", []string{b2})
	c1 := newClient(t)
	env.login(t, c1, "c1@vanta.test", "secret123")
	c2 := newClient(t)
	env.login(t, c2, "c2@vanta.test", "secret123")

	// Cada sucursal crea una requisición.
	env.do(t, c1, http.MethodPost, "/requisitions/supplies", map[string]any{
		"items": []map[string]any{{"supplyId": harina, "quantityBase": 10}},
	}).Body.Close()
	env.do(t, c2, http.MethodPost, "/requisitions/supplies", map[string]any{
		"items": []map[string]any{{"supplyId": harina, "quantityBase": 20}},
	}).Body.Close()

	// c1 solo ve la suya.
	resp := env.do(t, c1, http.MethodGet, "/requisitions/supplies", nil)
	var list struct {
		Items []struct {
			BranchID string `json:"branchId"`
		} `json:"items"`
	}
	decode(t, resp, &list)
	if len(list.Items) != 1 || list.Items[0].BranchID != b1 {
		t.Fatalf("c1 scope: esperaba 1 requisición de b1, obtuvo %+v", list.Items)
	}

	// super admin ve las 2.
	resp = env.do(t, root, http.MethodGet, "/requisitions/supplies", nil)
	decode(t, resp, &list)
	if len(list.Items) != 2 {
		t.Fatalf("super admin: esperaba 2 requisiciones, obtuvo %d", len(list.Items))
	}

	// repostero => 403 al crear y listar.
	env.createRepostero(t, root, "repo@vanta.test")
	repo := newClient(t)
	env.login(t, repo, "repo@vanta.test", "secret123")
	if resp := env.do(t, repo, http.MethodGet, "/requisitions/supplies", nil); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("repostero list: esperaba 403, obtuvo %d", resp.StatusCode)
	}
	if resp := env.do(t, repo, http.MethodPost, "/requisitions/supplies", map[string]any{
		"items": []map[string]any{{"supplyId": harina, "quantityBase": 1}},
	}); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("repostero create: esperaba 403, obtuvo %d", resp.StatusCode)
	}
	// super admin no crea requisiciones (403).
	if resp := env.do(t, root, http.MethodPost, "/requisitions/supplies", map[string]any{
		"items": []map[string]any{{"supplyId": harina, "quantityBase": 1}},
	}); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("super admin create: esperaba 403, obtuvo %d", resp.StatusCode)
	}

	// Sugerencias: sembramos stock de sucursal b1 para harina bajo mínimo y azúcar sobre
	// mínimo; solo harina debe sugerirse.
	env.pool.Exec(ctx,
		`INSERT INTO supply_branch_stock (tenant_id, supply_id, branch_id, stock_base, min_quantity, max_quantity)
		 VALUES ($1,$2,$3,5,10,40)`, env.tenantID, harina, b1)
	env.pool.Exec(ctx,
		`INSERT INTO supply_branch_stock (tenant_id, supply_id, branch_id, stock_base, min_quantity, max_quantity)
		 VALUES ($1,$2,$3,100,10,40)`, env.tenantID, azucar, b1)

	resp = env.do(t, c1, http.MethodGet, "/requisitions/supplies/suggestions", nil)
	var sug struct {
		Items []struct {
			SupplyID     string `json:"supplyId"`
			SuggestedQty int    `json:"suggestedQty"`
		} `json:"items"`
	}
	decode(t, resp, &sug)
	if len(sug.Items) != 1 || sug.Items[0].SupplyID != harina || sug.Items[0].SuggestedQty != 35 {
		t.Fatalf("suggestions: esperaba 1 (harina, 35 = max40 - stock5), obtuvo %+v", sug.Items)
	}
	// repostero no ve sugerencias (403).
	if resp := env.do(t, repo, http.MethodGet, "/requisitions/supplies/suggestions", nil); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("repostero suggestions: esperaba 403, obtuvo %d", resp.StatusCode)
	}
}
