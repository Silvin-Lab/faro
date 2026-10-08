package server_test

import (
	"context"
	"net/http"
	"testing"
)

// TestWarehouseWasteRoles cubre la apertura de merma a sucursal (M11): cajero/barista
// registran y listan merma de SU sucursal; branch_admin no puede afectar otra sucursal ni,
// al omitir branchId, el almacén central (se fuerza su sucursal activa); repostero sigue en
// 403 en merma (salvo GET /stock).
func TestWarehouseWasteRoles(t *testing.T) {
	env := setupM7(t)
	defer env.close()
	ctx := context.Background()

	root := newClient(t)
	env.login(t, root, "root@faro.test", "secret123")
	b1 := env.createBranch(t, root, "Centro")
	b2 := env.createBranch(t, root, "Norte")
	harina := env.createWarehouseSupply(t, root, "Harina", "g", "Bolsa 1000", 1)
	env.adjustWarehouse(t, root, harina, 100000)
	// Despacha stock a b1 y b2 para que haya existencia de sucursal.
	env.do(t, root, http.MethodPost, "/warehouse/dispatches", map[string]any{"supplyId": harina, "branchId": b1, "quantityBase": 1000}).Body.Close()
	env.do(t, root, http.MethodPost, "/warehouse/dispatches", map[string]any{"supplyId": harina, "branchId": b2, "quantityBase": 1000}).Body.Close()
	whBefore := env.warehouseStockOf(t, harina)

	branchWaste := func(branchID string) int {
		t.Helper()
		var n int
		env.pool.QueryRow(ctx,
			`SELECT COALESCE(SUM(quantity_base),0)::int FROM supply_movements
			  WHERE supply_id=$1 AND branch_id=$2 AND type='waste'`, harina, branchID).Scan(&n)
		return n
	}

	// --- Cajero de b1: crea merma de su sucursal (sin branchId) -----------------
	env.newBranchUser(t, root, "cajero@vanta.test", "cashier", []string{b1})
	cajero := newClient(t)
	env.login(t, cajero, "cajero@vanta.test", "secret123")
	if resp := env.do(t, cajero, http.MethodPost, "/warehouse/waste", map[string]any{
		"supplyId": harina, "quantityBase": 10, "reason": "se cayó",
	}); resp.StatusCode != http.StatusCreated {
		t.Fatalf("cajero merma: esperaba 201, obtuvo %d", resp.StatusCode)
	}
	if got := branchWaste(b1); got != -10 {
		t.Fatalf("cajero merma en b1: esperaba -10, obtuvo %d", got)
	}
	// El almacén central NO se tocó.
	if got := env.warehouseStockOf(t, harina); got != whBefore {
		t.Fatalf("cajero merma tocó el almacén central: %d != %d", got, whBefore)
	}
	// Cajero lista solo la merma de su sucursal.
	resp := env.do(t, cajero, http.MethodGet, "/warehouse/waste", nil)
	var wl struct {
		Items []struct {
			BranchID *string `json:"branchId"`
			Origin   string  `json:"origin"`
		} `json:"items"`
	}
	decode(t, resp, &wl)
	if len(wl.Items) != 1 || wl.Items[0].Origin != "branch" || wl.Items[0].BranchID == nil || *wl.Items[0].BranchID != b1 {
		t.Fatalf("cajero list merma: esperaba 1 fila de b1, obtuvo %+v", wl.Items)
	}

	// --- branch_admin de b1: no puede afectar b2 ni el almacén central ----------
	env.newBranchUser(t, root, "admin@vanta.test", "branch_admin", []string{b1})
	badmin := newClient(t)
	env.login(t, badmin, "admin@vanta.test", "secret123")
	// Manda branchId de b2 => se fuerza a b1.
	if resp := env.do(t, badmin, http.MethodPost, "/warehouse/waste", map[string]any{
		"supplyId": harina, "quantityBase": 7, "reason": "otra", "branchId": b2,
	}); resp.StatusCode != http.StatusCreated {
		t.Fatalf("branch_admin merma: esperaba 201, obtuvo %d", resp.StatusCode)
	}
	if got := branchWaste(b2); got != 0 {
		t.Fatalf("branch_admin NO debía afectar b2, obtuvo %d", got)
	}
	if got := branchWaste(b1); got != -17 {
		t.Fatalf("branch_admin merma debía ir a b1 (-10-7=-17), obtuvo %d", got)
	}
	if got := env.warehouseStockOf(t, harina); got != whBefore {
		t.Fatalf("branch_admin merma tocó el almacén central: %d != %d", got, whBefore)
	}

	// --- Repostero: 403 en merma (crear/listar); 200 en GET /stock -------------
	env.createRepostero(t, root, "repo@vanta.test")
	repo := newClient(t)
	env.login(t, repo, "repo@vanta.test", "secret123")
	if resp := env.do(t, repo, http.MethodPost, "/warehouse/waste", map[string]any{
		"supplyId": harina, "quantityBase": 1, "reason": "x",
	}); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("repostero crear merma: esperaba 403, obtuvo %d", resp.StatusCode)
	}
	if resp := env.do(t, repo, http.MethodGet, "/warehouse/waste", nil); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("repostero list merma: esperaba 403, obtuvo %d", resp.StatusCode)
	}
	if resp := env.do(t, repo, http.MethodGet, "/warehouse/stock", nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("repostero GET /stock: esperaba 200, obtuvo %d", resp.StatusCode)
	}

	// --- Super admin: sin branchId => merma del almacén central -----------------
	if resp := env.do(t, root, http.MethodPost, "/warehouse/waste", map[string]any{
		"supplyId": harina, "quantityBase": 25, "reason": "caducó en almacén",
	}); resp.StatusCode != http.StatusCreated {
		t.Fatalf("super admin merma almacén: esperaba 201, obtuvo %d", resp.StatusCode)
	}
	if got := env.warehouseStockOf(t, harina); got != whBefore-25 {
		t.Fatalf("super admin merma almacén: esperaba %d, obtuvo %d", whBefore-25, got)
	}
}
