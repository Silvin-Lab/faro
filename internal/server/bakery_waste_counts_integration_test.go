package server_test

import (
	"net/http"
	"testing"
)

// seedBakeryStock crea un pedido (cajero) y lo produce (repostero) para dejar `qty` unidades
// del postre `productID` en la sucursal activa del cajero.
func (e *m7Env) seedBakeryStock(t *testing.T, cajero, repo *http.Client, qty int, productID string) {
	t.Helper()
	resp := e.do(t, cajero, http.MethodPost, "/bakery/orders", map[string]any{"productId": productID, "quantity": qty})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("seed pedido: esperaba 201, obtuvo %d", resp.StatusCode)
	}
	var o struct {
		Order struct {
			ID string `json:"id"`
		} `json:"order"`
	}
	decode(t, resp, &o)
	e.do(t, repo, http.MethodPost, "/bakery/orders/"+o.Order.ID+"/produce", map[string]any{"quantity": qty}).Body.Close()
}

// TestBakeryWaste: la sucursal registra merma de postre; baja el stock; repostero => 403;
// super_admin exige branchId.
func TestBakeryWaste(t *testing.T) {
	env := setupM7(t)
	defer env.close()

	root := newClient(t)
	env.login(t, root, "root@faro.test", "secret123")
	b1 := env.createBranch(t, root, "Centro")
	cheesecake := env.createBakeryProduct(t, root, "Cheesecake", 8000)
	env.createRepostero(t, root, "repo@vanta.test")
	env.newBranchUser(t, root, "cajero@vanta.test", "cashier", []string{b1})
	repo := newClient(t)
	env.login(t, repo, "repo@vanta.test", "secret123")
	cajero := newClient(t)
	env.login(t, cajero, "cajero@vanta.test", "secret123")

	env.seedBakeryStock(t, cajero, repo, 10, cheesecake)
	if got := env.productBranchStockOf(t, cheesecake, b1); got != 10 {
		t.Fatalf("stock inicial: esperaba 10, obtuvo %d", got)
	}

	// Cajero registra merma de 3.
	resp := env.do(t, cajero, http.MethodPost, "/bakery/waste", map[string]any{
		"productId": cheesecake, "quantity": 3, "reason": "se cayó",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("merma postre: esperaba 201, obtuvo %d", resp.StatusCode)
	}
	var wm struct {
		Movement struct {
			Quantity int `json:"quantity"`
		} `json:"movement"`
		StockQty int `json:"stockQty"`
	}
	decode(t, resp, &wm)
	if wm.Movement.Quantity != -3 || wm.StockQty != 7 {
		t.Fatalf("merma postre: esperaba quantity=-3 stockQty=7, obtuvo %+v", wm)
	}
	if got := env.productBranchStockOf(t, cheesecake, b1); got != 7 {
		t.Fatalf("stock tras merma: esperaba 7, obtuvo %d", got)
	}
	env.assertProductStockInvariant(t, cheesecake, b1)

	// Repostero NO puede registrar merma (403).
	if resp := env.do(t, repo, http.MethodPost, "/bakery/waste", map[string]any{
		"productId": cheesecake, "quantity": 1, "reason": "x",
	}); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("repostero merma: esperaba 403, obtuvo %d", resp.StatusCode)
	}
	// Super admin sin branchId => 400.
	if resp := env.do(t, root, http.MethodPost, "/bakery/waste", map[string]any{
		"productId": cheesecake, "quantity": 1, "reason": "x",
	}); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("super admin merma sin branchId: esperaba 400, obtuvo %d", resp.StatusCode)
	}
	// Super admin con branchId => 201.
	if resp := env.do(t, root, http.MethodPost, "/bakery/waste", map[string]any{
		"productId": cheesecake, "quantity": 1, "reason": "x", "branchId": b1,
	}); resp.StatusCode != http.StatusCreated {
		t.Fatalf("super admin merma con branchId: esperaba 201, obtuvo %d", resp.StatusCode)
	}

	// Cajero lista solo la merma de su sucursal (2 movimientos).
	resp = env.do(t, cajero, http.MethodGet, "/bakery/waste", nil)
	var wl struct {
		Items []struct {
			Quantity int `json:"quantity"`
		} `json:"items"`
	}
	decode(t, resp, &wl)
	if len(wl.Items) != 2 {
		t.Fatalf("historial merma postre: esperaba 2, obtuvo %d", len(wl.Items))
	}
}

// TestBakeryCount: conteo de cierre reconcilia (merma si falta, ajuste si sobra, nada si
// coincide) y deja el cache en el valor contado.
func TestBakeryCount(t *testing.T) {
	env := setupM7(t)
	defer env.close()

	root := newClient(t)
	env.login(t, root, "root@faro.test", "secret123")
	b1 := env.createBranch(t, root, "Centro")
	pastel := env.createBakeryProduct(t, root, "Pastel", 9000)
	flan := env.createBakeryProduct(t, root, "Flan", 5000)
	galleta := env.createBakeryProduct(t, root, "Galleta", 2000)
	env.createRepostero(t, root, "repo@vanta.test")
	env.newBranchUser(t, root, "cajero@vanta.test", "cashier", []string{b1})
	repo := newClient(t)
	env.login(t, repo, "repo@vanta.test", "secret123")
	cajero := newClient(t)
	env.login(t, cajero, "cajero@vanta.test", "secret123")

	// Stock inicial: pastel 10, flan 5, galleta 8.
	env.seedBakeryStock(t, cajero, repo, 10, pastel)
	env.seedBakeryStock(t, cajero, repo, 5, flan)
	env.seedBakeryStock(t, cajero, repo, 8, galleta)

	// Conteo: pastel 7 (faltan 3 => waste), flan 6 (sobra 1 => adjustment), galleta 8 (igual).
	resp := env.do(t, cajero, http.MethodPost, "/bakery/counts", map[string]any{
		"note": "cierre",
		"items": []map[string]any{
			{"productId": pastel, "countedQty": 7},
			{"productId": flan, "countedQty": 6},
			{"productId": galleta, "countedQty": 8},
		},
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("conteo: esperaba 201, obtuvo %d", resp.StatusCode)
	}
	var cd struct {
		Count struct {
			ID string `json:"id"`
		} `json:"count"`
		Items []struct {
			ProductID    string  `json:"productId"`
			ExpectedQty  int     `json:"expectedQty"`
			CountedQty   int     `json:"countedQty"`
			DiffQty      int     `json:"diffQty"`
			MovementType *string `json:"movementType"`
		} `json:"items"`
	}
	decode(t, resp, &cd)
	byProduct := map[string]struct {
		diff int
		mt   *string
	}{}
	for _, it := range cd.Items {
		byProduct[it.ProductID] = struct {
			diff int
			mt   *string
		}{it.DiffQty, it.MovementType}
	}
	if p := byProduct[pastel]; p.diff != -3 || p.mt == nil || *p.mt != "waste" {
		t.Fatalf("pastel: esperaba diff=-3 waste, obtuvo diff=%d mt=%v", p.diff, p.mt)
	}
	if f := byProduct[flan]; f.diff != 1 || f.mt == nil || *f.mt != "adjustment" {
		t.Fatalf("flan: esperaba diff=1 adjustment, obtuvo diff=%d mt=%v", f.diff, f.mt)
	}
	if g := byProduct[galleta]; g.diff != 0 || g.mt != nil {
		t.Fatalf("galleta: esperaba diff=0 sin movimiento, obtuvo diff=%d mt=%v", g.diff, g.mt)
	}

	// El cache quedó en lo contado y los invariantes se mantienen.
	if got := env.productBranchStockOf(t, pastel, b1); got != 7 {
		t.Fatalf("pastel cache: esperaba 7, obtuvo %d", got)
	}
	if got := env.productBranchStockOf(t, flan, b1); got != 6 {
		t.Fatalf("flan cache: esperaba 6, obtuvo %d", got)
	}
	if got := env.productBranchStockOf(t, galleta, b1); got != 8 {
		t.Fatalf("galleta cache: esperaba 8, obtuvo %d", got)
	}
	env.assertProductStockInvariant(t, pastel, b1)
	env.assertProductStockInvariant(t, flan, b1)
	env.assertProductStockInvariant(t, galleta, b1)

	// Detalle del conteo accesible para la sucursal dueña.
	if resp := env.do(t, cajero, http.MethodGet, "/bakery/counts/"+cd.Count.ID, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("detalle conteo: esperaba 200, obtuvo %d", resp.StatusCode)
	}
	// Repostero puede leer conteos (producción ve todo).
	if resp := env.do(t, repo, http.MethodGet, "/bakery/counts/"+cd.Count.ID, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("repostero detalle conteo: esperaba 200, obtuvo %d", resp.StatusCode)
	}
}
