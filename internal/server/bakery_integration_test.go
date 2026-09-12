package server_test

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"
)

// ---- Helpers específicos de bakery -----------------------------------------

// createBakeryProduct crea un producto de repostería (fulfillment_type=bakery) y devuelve
// su id.
func (e *m7Env) createBakeryProduct(t *testing.T, c *http.Client, name string, priceCents int) string {
	t.Helper()
	resp := e.do(t, c, http.MethodPost, "/products", map[string]any{
		"name": name, "priceCents": priceCents, "fulfillmentType": "bakery",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /products %s bakery: esperaba 201, obtuvo %d", name, resp.StatusCode)
	}
	var body struct {
		Product struct {
			ID              string `json:"id"`
			FulfillmentType string `json:"fulfillmentType"`
		} `json:"product"`
	}
	decode(t, resp, &body)
	if body.Product.FulfillmentType != "bakery" {
		t.Fatalf("producto %s: esperaba fulfillmentType=bakery, obtuvo %q", name, body.Product.FulfillmentType)
	}
	return body.Product.ID
}

// setRecipe fija la receta de un producto (supplyID -> quantityBase por unidad).
func (e *m7Env) setRecipe(t *testing.T, c *http.Client, productID string, lines map[string]int) {
	t.Helper()
	items := []map[string]any{}
	for supplyID, qty := range lines {
		items = append(items, map[string]any{"supplyId": supplyID, "quantityBase": qty})
	}
	resp := e.do(t, c, http.MethodPut, "/supplies/recipes/"+productID, map[string]any{"items": items})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT recipe %s: esperaba 200, obtuvo %d", productID, resp.StatusCode)
	}
	resp.Body.Close()
}

// adjustWarehouse fija la existencia del almacén de un insumo a newQty.
func (e *m7Env) adjustWarehouse(t *testing.T, c *http.Client, supplyID string, newQty int) {
	t.Helper()
	resp := e.do(t, c, http.MethodPost, "/warehouse/stock/"+supplyID+"/adjust", map[string]any{"newQuantity": newQty})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("adjust warehouse %s: esperaba 200, obtuvo %d", supplyID, resp.StatusCode)
	}
	resp.Body.Close()
}

// warehouseStockOf devuelve el stock_base del almacén para un insumo (0 si no hay fila).
func (e *m7Env) warehouseStockOf(t *testing.T, supplyID string) int {
	t.Helper()
	var n int
	err := e.pool.QueryRow(context.Background(),
		`SELECT COALESCE((SELECT stock_base FROM warehouse_stock WHERE supply_id=$1), 0)`, supplyID).Scan(&n)
	if err != nil {
		t.Fatalf("warehouseStockOf: %v", err)
	}
	return n
}

// productBranchStockOf devuelve el stock de postre de (producto, sucursal) (0 si no hay fila).
func (e *m7Env) productBranchStockOf(t *testing.T, productID, branchID string) int {
	t.Helper()
	var n int
	err := e.pool.QueryRow(context.Background(),
		`SELECT COALESCE((SELECT stock_qty FROM product_branch_stock WHERE product_id=$1 AND branch_id=$2), 0)`,
		productID, branchID).Scan(&n)
	if err != nil {
		t.Fatalf("productBranchStockOf: %v", err)
	}
	return n
}

// assertWarehouseInvariant verifica stock_base == SUM(warehouse_movements) para un insumo.
func (e *m7Env) assertWarehouseInvariant(t *testing.T, supplyID string) {
	t.Helper()
	var cache, ledger int
	ctx := context.Background()
	e.pool.QueryRow(ctx, `SELECT COALESCE((SELECT stock_base FROM warehouse_stock WHERE supply_id=$1),0)`, supplyID).Scan(&cache)
	e.pool.QueryRow(ctx, `SELECT COALESCE(SUM(quantity_base),0)::int FROM warehouse_movements WHERE supply_id=$1`, supplyID).Scan(&ledger)
	if cache != ledger {
		t.Fatalf("invariante almacén %s: cache=%d != ledger=%d", supplyID, cache, ledger)
	}
}

// assertProductStockInvariant verifica stock_qty == SUM(product_stock_movements) para
// (producto, sucursal).
func (e *m7Env) assertProductStockInvariant(t *testing.T, productID, branchID string) {
	t.Helper()
	var cache, ledger int
	ctx := context.Background()
	e.pool.QueryRow(ctx, `SELECT COALESCE((SELECT stock_qty FROM product_branch_stock WHERE product_id=$1 AND branch_id=$2),0)`, productID, branchID).Scan(&cache)
	e.pool.QueryRow(ctx, `SELECT COALESCE(SUM(quantity),0)::int FROM product_stock_movements WHERE product_id=$1 AND branch_id=$2`, productID, branchID).Scan(&ledger)
	if cache != ledger {
		t.Fatalf("invariante stock postre (%s,%s): cache=%d != ledger=%d", productID, branchID, cache, ledger)
	}
}

// createRepostero da de alta un repostero (sin sucursal) y devuelve su id.
func (e *m7Env) createRepostero(t *testing.T, c *http.Client, email string) string {
	t.Helper()
	status, u := e.createUser(t, c, map[string]any{
		"email": email, "password": "secret123", "name": "Repostero", "role": "repostero",
	})
	if status != http.StatusCreated {
		t.Fatalf("crear repostero: esperaba 201, obtuvo %d", status)
	}
	if u.Role != "repostero" || u.IsSuperAdmin || u.TenantID == nil || len(u.Branches) != 0 {
		t.Fatalf("repostero creado inesperado: %+v", u)
	}
	return u.ID
}

// ---- Ciclo de vida completo del pedido -------------------------------------

// TestBakeryOrderLifecycle cubre el flujo crítico: crear pedido -> producir parcial ->
// producir hasta completar -> shipped -> recibido, verificando en cada paso los dos
// ledgers (almacén y stock de postre) y sus invariantes.
func TestBakeryOrderLifecycle(t *testing.T) {
	env := setupM7(t)
	defer env.close()

	root := newClient(t)
	env.login(t, root, "root@faro.test", "secret123")
	b1 := env.createBranch(t, root, "Centro")

	// Insumo con stock inicial 100000 (unidad base) y postre con receta 10/unidad.
	harina := env.createWarehouseSupply(t, root, "Harina", "g", "Bolsa 1000", 1)
	env.adjustWarehouse(t, root, harina, 100000)
	cheesecake := env.createBakeryProduct(t, root, "Cheesecake", 8000)
	env.setRecipe(t, root, cheesecake, map[string]int{harina: 10})

	// Repostero (produce) y cajero de b1 (pide).
	env.createRepostero(t, root, "repo@vanta.test")
	env.newBranchUser(t, root, "cajero@vanta.test", "cashier", []string{b1})
	repo := newClient(t)
	env.login(t, repo, "repo@vanta.test", "secret123")
	cajero := newClient(t)
	env.login(t, cajero, "cajero@vanta.test", "secret123")

	// --- Crear pedido de 10 (sucursal) ------------------------------------
	resp := env.do(t, cajero, http.MethodPost, "/bakery/orders", map[string]any{
		"productId": cheesecake, "quantity": 10, "note": "para el finde",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("crear pedido: esperaba 201, obtuvo %d", resp.StatusCode)
	}
	var created struct {
		Order struct {
			ID              string `json:"id"`
			Status          string `json:"status"`
			QuantityShipped int    `json:"quantityShipped"`
			BranchID        string `json:"branchId"`
		} `json:"order"`
	}
	decode(t, resp, &created)
	orderID := created.Order.ID
	if created.Order.Status != "pending" || created.Order.QuantityShipped != 0 || created.Order.BranchID != b1 {
		t.Fatalf("pedido creado inesperado: %+v", created.Order)
	}

	// El pedido aparece en la cola del repostero (todas las sucursales).
	resp = env.do(t, repo, http.MethodGet, "/bakery/orders?status=pending", nil)
	var queue struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	decode(t, resp, &queue)
	if len(queue.Items) != 1 || queue.Items[0].ID != orderID {
		t.Fatalf("cola del repostero: esperaba el pedido %s, obtuvo %+v", orderID, queue.Items)
	}

	// --- Producción parcial: 6 de 10 -> in_production ---------------------
	resp = env.do(t, repo, http.MethodPost, "/bakery/orders/"+orderID+"/produce", map[string]any{"quantity": 6})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("produce 6: esperaba 200, obtuvo %d", resp.StatusCode)
	}
	var prod struct {
		Order struct {
			Status          string `json:"status"`
			QuantityShipped int    `json:"quantityShipped"`
		} `json:"order"`
	}
	decode(t, resp, &prod)
	if prod.Order.Status != "in_production" || prod.Order.QuantityShipped != 6 {
		t.Fatalf("tras produce 6: esperaba in_production/6, obtuvo %+v", prod.Order)
	}
	if got := env.warehouseStockOf(t, harina); got != 100000-60 {
		t.Fatalf("almacén tras produce 6: esperaba %d, obtuvo %d", 100000-60, got)
	}
	if got := env.productBranchStockOf(t, cheesecake, b1); got != 6 {
		t.Fatalf("stock postre tras produce 6: esperaba 6, obtuvo %d", got)
	}
	env.assertWarehouseInvariant(t, harina)
	env.assertProductStockInvariant(t, cheesecake, b1)

	// --- Producción final: 4 -> shipped ----------------------------------
	resp = env.do(t, repo, http.MethodPost, "/bakery/orders/"+orderID+"/produce", map[string]any{"quantity": 4})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("produce 4: esperaba 200, obtuvo %d", resp.StatusCode)
	}
	decode(t, resp, &prod)
	if prod.Order.Status != "shipped" || prod.Order.QuantityShipped != 10 {
		t.Fatalf("tras produce 4: esperaba shipped/10, obtuvo %+v", prod.Order)
	}
	if got := env.warehouseStockOf(t, harina); got != 100000-100 {
		t.Fatalf("almacén tras produce 10: esperaba %d, obtuvo %d", 100000-100, got)
	}
	if got := env.productBranchStockOf(t, cheesecake, b1); got != 10 {
		t.Fatalf("stock postre tras produce 10: esperaba 10, obtuvo %d", got)
	}
	env.assertWarehouseInvariant(t, harina)
	env.assertProductStockInvariant(t, cheesecake, b1)

	// --- No se puede producir sobre shipped -> 409 invalid_state ----------
	resp = env.do(t, repo, http.MethodPost, "/bakery/orders/"+orderID+"/produce", map[string]any{"quantity": 1})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("produce sobre shipped: esperaba 409, obtuvo %d", resp.StatusCode)
	}
	resp.Body.Close()

	// --- La sucursal marca recibido; no mueve stock ----------------------
	stockBefore := env.productBranchStockOf(t, cheesecake, b1)
	resp = env.do(t, cajero, http.MethodPatch, "/bakery/orders/"+orderID+"/receive", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("receive: esperaba 200, obtuvo %d", resp.StatusCode)
	}
	var recv struct {
		Order struct {
			Status string `json:"status"`
		} `json:"order"`
	}
	decode(t, resp, &recv)
	if recv.Order.Status != "received" {
		t.Fatalf("receive: esperaba received, obtuvo %q", recv.Order.Status)
	}
	if got := env.productBranchStockOf(t, cheesecake, b1); got != stockBefore {
		t.Fatalf("receive movió stock: antes %d, después %d", stockBefore, got)
	}
}

// TestBakeryProduceWithoutRecipe: un postre sin receta acredita el postre y NO escribe
// movimientos de almacén (no-op natural).
func TestBakeryProduceWithoutRecipe(t *testing.T) {
	env := setupM7(t)
	defer env.close()

	root := newClient(t)
	env.login(t, root, "root@faro.test", "secret123")
	b1 := env.createBranch(t, root, "Centro")
	flan := env.createBakeryProduct(t, root, "Flan", 5000)
	env.createRepostero(t, root, "repo@vanta.test")
	env.newBranchUser(t, root, "cajero@vanta.test", "cashier", []string{b1})
	repo := newClient(t)
	env.login(t, repo, "repo@vanta.test", "secret123")
	cajero := newClient(t)
	env.login(t, cajero, "cajero@vanta.test", "secret123")

	resp := env.do(t, cajero, http.MethodPost, "/bakery/orders", map[string]any{"productId": flan, "quantity": 5})
	var created struct {
		Order struct{ ID string `json:"id"` } `json:"order"`
	}
	decode(t, resp, &created)

	resp = env.do(t, repo, http.MethodPost, "/bakery/orders/"+created.Order.ID+"/produce", map[string]any{"quantity": 5})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("produce sin receta: esperaba 200, obtuvo %d", resp.StatusCode)
	}
	resp.Body.Close()
	if got := env.productBranchStockOf(t, flan, b1); got != 5 {
		t.Fatalf("stock postre sin receta: esperaba 5, obtuvo %d", got)
	}
	var whMovs int
	env.pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM warehouse_movements WHERE type='production'`).Scan(&whMovs)
	if whMovs != 0 {
		t.Fatalf("produce sin receta: esperaba 0 movimientos de almacén, obtuvo %d", whMovs)
	}
}

// ---- Cancelación (solo pending) --------------------------------------------

func TestBakeryCancel(t *testing.T) {
	env := setupM7(t)
	defer env.close()

	root := newClient(t)
	env.login(t, root, "root@faro.test", "secret123")
	b1 := env.createBranch(t, root, "Centro")
	postre := env.createBakeryProduct(t, root, "Brownie", 3000)
	env.createRepostero(t, root, "repo@vanta.test")
	env.newBranchUser(t, root, "cajero@vanta.test", "cashier", []string{b1})
	repo := newClient(t)
	env.login(t, repo, "repo@vanta.test", "secret123")
	cajero := newClient(t)
	env.login(t, cajero, "cajero@vanta.test", "secret123")

	// Pedido pending -> cancelable.
	resp := env.do(t, cajero, http.MethodPost, "/bakery/orders", map[string]any{"productId": postre, "quantity": 4})
	var o1 struct{ Order struct{ ID string `json:"id"` } `json:"order"` }
	decode(t, resp, &o1)
	if resp := env.do(t, cajero, http.MethodPatch, "/bakery/orders/"+o1.Order.ID+"/cancel", nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("cancel pending: esperaba 200, obtuvo %d", resp.StatusCode)
	}

	// Pedido con producción -> NO cancelable (409).
	resp = env.do(t, cajero, http.MethodPost, "/bakery/orders", map[string]any{"productId": postre, "quantity": 4})
	var o2 struct{ Order struct{ ID string `json:"id"` } `json:"order"` }
	decode(t, resp, &o2)
	env.do(t, repo, http.MethodPost, "/bakery/orders/"+o2.Order.ID+"/produce", map[string]any{"quantity": 2}).Body.Close()
	if resp := env.do(t, cajero, http.MethodPatch, "/bakery/orders/"+o2.Order.ID+"/cancel", nil); resp.StatusCode != http.StatusConflict {
		t.Fatalf("cancel in_production: esperaba 409, obtuvo %d", resp.StatusCode)
	}
}

// ---- Concurrencia en /produce ----------------------------------------------

// TestBakeryProduceConcurrency verifica que dos producciones simultáneas contra el mismo
// pedido no corrompen quantity_shipped: FOR UPDATE serializa; el acumulado es exacto.
func TestBakeryProduceConcurrency(t *testing.T) {
	env := setupM7(t)
	defer env.close()

	root := newClient(t)
	env.login(t, root, "root@faro.test", "secret123")
	b1 := env.createBranch(t, root, "Centro")
	harina := env.createWarehouseSupply(t, root, "Harina", "g", "Bolsa 1000", 1)
	env.adjustWarehouse(t, root, harina, 100000)
	postre := env.createBakeryProduct(t, root, "Cheesecake", 8000)
	env.setRecipe(t, root, postre, map[string]int{harina: 10})
	env.createRepostero(t, root, "repo@vanta.test")
	env.newBranchUser(t, root, "cajero@vanta.test", "cashier", []string{b1})
	repo := newClient(t)
	env.login(t, repo, "repo@vanta.test", "secret123")
	cajero := newClient(t)
	env.login(t, cajero, "cajero@vanta.test", "secret123")

	resp := env.do(t, cajero, http.MethodPost, "/bakery/orders", map[string]any{"productId": postre, "quantity": 10})
	var o struct{ Order struct{ ID string `json:"id"` } `json:"order"` }
	decode(t, resp, &o)
	orderID := o.Order.ID

	// Dos produce de 6 concurrentes: 6+6=12 >= 10 => ambas válidas, shipped, qty=12.
	var wg sync.WaitGroup
	codes := make([]int, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			r := env.do(t, repo, http.MethodPost, "/bakery/orders/"+orderID+"/produce", map[string]any{"quantity": 6})
			codes[idx] = r.StatusCode
			r.Body.Close()
		}(i)
	}
	wg.Wait()

	for i, code := range codes {
		if code != http.StatusOK {
			t.Fatalf("produce concurrente %d: esperaba 200, obtuvo %d", i, code)
		}
	}
	var shipped int
	var status string
	env.pool.QueryRow(context.Background(),
		`SELECT quantity_shipped, status FROM bakery_orders WHERE id=$1`, orderID).Scan(&shipped, &status)
	if shipped != 12 || status != "shipped" {
		t.Fatalf("concurrencia: esperaba quantity_shipped=12 status=shipped, obtuvo %d/%s", shipped, status)
	}
	if got := env.productBranchStockOf(t, postre, b1); got != 12 {
		t.Fatalf("concurrencia: stock postre esperaba 12, obtuvo %d", got)
	}
	if got := env.warehouseStockOf(t, harina); got != 100000-120 {
		t.Fatalf("concurrencia: almacén esperaba %d, obtuvo %d", 100000-120, got)
	}
	env.assertWarehouseInvariant(t, harina)
	env.assertProductStockInvariant(t, postre, b1)
}

// ---- Descuento en la venta (bifurcación por fulfillment_type) --------------

// TestBakerySaleDeductsFinishedGoods: vender un postre descuenta su stock de postre y NO
// insumos; un carrito mixto reparte correctamente.
func TestBakerySaleDeductsFinishedGoods(t *testing.T) {
	env := setupM7(t)
	defer env.close()

	root := newClient(t)
	env.login(t, root, "root@faro.test", "secret123")
	b1 := env.createBranch(t, root, "Centro")

	// Insumo compartido; postre 'bakery' (receta 10/u, consumida al producir) y café
	// 'branch_prepared' (receta 5/u, consumida al vender).
	harina := env.createWarehouseSupply(t, root, "Harina", "g", "Bolsa 1000", 1)
	env.adjustWarehouse(t, root, harina, 100000)
	// Stock de sucursal del insumo para que el café descuente de ahí (supply_branch_stock):
	// se despacha del almacén a la sucursal.
	env.do(t, root, http.MethodPost, "/warehouse/suppliers", map[string]any{"name": "Prov"}).Body.Close()

	cheesecake := env.createBakeryProduct(t, root, "Cheesecake", 8000)
	env.setRecipe(t, root, cheesecake, map[string]int{harina: 10})

	// Café branch_prepared con receta.
	var cafe string
	env.pool.QueryRow(context.Background(),
		"INSERT INTO products (tenant_id, name, price_cents) VALUES ($1,'Cafe',4000) RETURNING id::text", env.tenantID).Scan(&cafe)
	env.setRecipe(t, root, cafe, map[string]int{harina: 5})

	// Repostero produce 10 cheesecakes para b1.
	env.createRepostero(t, root, "repo@vanta.test")
	env.newBranchUser(t, root, "cajero@vanta.test", "cashier", []string{b1})
	repo := newClient(t)
	env.login(t, repo, "repo@vanta.test", "secret123")
	cajero := newClient(t)
	env.login(t, cajero, "cajero@vanta.test", "secret123")

	resp := env.do(t, cajero, http.MethodPost, "/bakery/orders", map[string]any{"productId": cheesecake, "quantity": 10})
	var o struct{ Order struct{ ID string `json:"id"` } `json:"order"` }
	decode(t, resp, &o)
	env.do(t, repo, http.MethodPost, "/bakery/orders/"+o.Order.ID+"/produce", map[string]any{"quantity": 10}).Body.Close()

	whAfterProduce := env.warehouseStockOf(t, harina) // 100000 - 100
	if whAfterProduce != 100000-100 {
		t.Fatalf("almacén tras producir: esperaba %d, obtuvo %d", 100000-100, whAfterProduce)
	}

	// Vender carrito mixto: 2 cheesecake (bakery) + 1 café (branch_prepared).
	resp = env.do(t, cajero, http.MethodPost, "/sales", map[string]any{
		"items": []map[string]any{
			{"productId": cheesecake, "quantity": 2},
			{"productId": cafe, "quantity": 1},
		},
		"paymentMethod": "cash", "amountPaidCents": 100000,
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("venta mixta: esperaba 201, obtuvo %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Stock de postre bajó 2 (10 -> 8); insumos del almacén NO cambian por vender el postre.
	if got := env.productBranchStockOf(t, cheesecake, b1); got != 8 {
		t.Fatalf("venta postre: stock esperaba 8, obtuvo %d", got)
	}
	if got := env.warehouseStockOf(t, harina); got != whAfterProduce {
		t.Fatalf("venta postre tocó el almacén: esperaba %d, obtuvo %d", whAfterProduce, got)
	}
	// El café (branch_prepared) descontó 5 de insumo de la SUCURSAL (supply_branch_stock,
	// no del almacén). Verificamos que existe el movimiento 'sale' de ese insumo por -5.
	var saleMov int
	env.pool.QueryRow(context.Background(),
		`SELECT COALESCE(SUM(quantity_base),0)::int FROM supply_movements WHERE supply_id=$1 AND type='sale'`, harina).Scan(&saleMov)
	if saleMov != -5 {
		t.Fatalf("venta café: esperaba movimiento de insumo -5, obtuvo %d", saleMov)
	}
	// El postre NO generó movimiento 'sale' de insumo (solo el café).
	env.assertWarehouseInvariant(t, harina)
	env.assertProductStockInvariant(t, cheesecake, b1)
}

// ---- Gating por rol de /bakery ---------------------------------------------

// TestBakeryGating verifica la matriz §7.4: sucursal no puede producir; repostero no puede
// crear pedido ni cancelar/recibir.
func TestBakeryGating(t *testing.T) {
	env := setupM7(t)
	defer env.close()

	root := newClient(t)
	env.login(t, root, "root@faro.test", "secret123")
	b1 := env.createBranch(t, root, "Centro")
	postre := env.createBakeryProduct(t, root, "Cheesecake", 8000)
	env.createRepostero(t, root, "repo@vanta.test")
	env.newBranchUser(t, root, "cajero@vanta.test", "cashier", []string{b1})
	repo := newClient(t)
	env.login(t, repo, "repo@vanta.test", "secret123")
	cajero := newClient(t)
	env.login(t, cajero, "cajero@vanta.test", "secret123")

	// Repostero NO puede crear pedido (403).
	if resp := env.do(t, repo, http.MethodPost, "/bakery/orders", map[string]any{"productId": postre, "quantity": 2}); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("repostero crear pedido: esperaba 403, obtuvo %d", resp.StatusCode)
	}
	// Sucursal crea un pedido.
	resp := env.do(t, cajero, http.MethodPost, "/bakery/orders", map[string]any{"productId": postre, "quantity": 4})
	var o struct{ Order struct{ ID string `json:"id"` } `json:"order"` }
	decode(t, resp, &o)

	// Sucursal NO puede producir (403).
	if resp := env.do(t, cajero, http.MethodPost, "/bakery/orders/"+o.Order.ID+"/produce", map[string]any{"quantity": 1}); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("sucursal producir: esperaba 403, obtuvo %d", resp.StatusCode)
	}
	// Repostero NO puede cancelar ni recibir (403).
	if resp := env.do(t, repo, http.MethodPatch, "/bakery/orders/"+o.Order.ID+"/cancel", nil); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("repostero cancelar: esperaba 403, obtuvo %d", resp.StatusCode)
	}
	if resp := env.do(t, repo, http.MethodPatch, "/bakery/orders/"+o.Order.ID+"/receive", nil); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("repostero recibir: esperaba 403, obtuvo %d", resp.StatusCode)
	}
	// Sucursal NO puede ver la auditoría de producciones (403).
	if resp := env.do(t, cajero, http.MethodGet, "/bakery/productions", nil); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("sucursal /productions: esperaba 403, obtuvo %d", resp.StatusCode)
	}
}

// ---- Regresión de autorización de /warehouse -------------------------------

// TestWarehouseStockRepostero verifica que el split del router abre GET /warehouse/stock a
// repostero (200) pero mantiene el resto de /warehouse en 403 para él; y que los 3 roles de
// sucursal siguen en 403 en TODO /warehouse (incl. GET /stock).
func TestWarehouseStockRepostero(t *testing.T) {
	env := setupM7(t)
	defer env.close()

	root := newClient(t)
	env.login(t, root, "root@faro.test", "secret123")
	b1 := env.createBranch(t, root, "Centro")
	env.createRepostero(t, root, "repo@vanta.test")
	repo := newClient(t)
	env.login(t, repo, "repo@vanta.test", "secret123")

	// Repostero: GET /warehouse/stock => 200.
	if resp := env.do(t, repo, http.MethodGet, "/warehouse/stock", nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("repostero GET /warehouse/stock: esperaba 200, obtuvo %d", resp.StatusCode)
	}
	// Repostero: el resto de /warehouse => 403.
	repoForbidden := []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, "/warehouse/to-buy", nil},
		{http.MethodGet, "/warehouse/suppliers", nil},
		{http.MethodPost, "/warehouse/suppliers", map[string]any{"name": "X"}},
		{http.MethodGet, "/warehouse/purchases", nil},
		{http.MethodPost, "/warehouse/purchases", map[string]any{"supplyId": "x", "supplierId": "y", "packages": 1, "unitCostCents": 1}},
		{http.MethodGet, "/warehouse/dispatches", nil},
		{http.MethodPost, "/warehouse/dispatches", map[string]any{"supplyId": "x", "branchId": "y", "quantityBase": 1}},
		{http.MethodGet, "/warehouse/waste", nil},
		{http.MethodPost, "/warehouse/waste", map[string]any{"supplyId": "x", "quantityBase": 1, "reason": "r"}},
		{http.MethodPatch, "/warehouse/stock/00000000-0000-0000-0000-000000000000", map[string]any{"minQuantity": 1}},
		{http.MethodPost, "/warehouse/stock/00000000-0000-0000-0000-000000000000/adjust", map[string]any{"newQuantity": 1}},
	}
	for _, rt := range repoForbidden {
		if resp := env.do(t, repo, rt.method, rt.path, rt.body); resp.StatusCode != http.StatusForbidden {
			t.Fatalf("repostero %s %s: esperaba 403, obtuvo %d", rt.method, rt.path, resp.StatusCode)
		}
	}

	// Los 3 roles de sucursal: 403 en TODO /warehouse, incl. GET /stock.
	for _, role := range []string{"branch_admin", "cashier", "barista"} {
		email := role + "@vanta.test"
		env.newBranchUser(t, root, email, role, []string{b1})
		c := newClient(t)
		env.login(t, c, email, "secret123")
		if resp := env.do(t, c, http.MethodGet, "/warehouse/stock", nil); resp.StatusCode != http.StatusForbidden {
			t.Fatalf("%s GET /warehouse/stock: esperaba 403, obtuvo %d", role, resp.StatusCode)
		}
		if resp := env.do(t, c, http.MethodGet, "/warehouse/suppliers", nil); resp.StatusCode != http.StatusForbidden {
			t.Fatalf("%s GET /warehouse/suppliers: esperaba 403, obtuvo %d", role, resp.StatusCode)
		}
	}
}

// ---- Invariante repostero-sin-sucursal (R4) --------------------------------

// TestReposteroNoBranchInvariant verifica el blindaje: no se puede crear un repostero con
// sucursal, ni asignarle una (ni cambiarle el rol) por UpdateUser.
func TestReposteroNoBranchInvariant(t *testing.T) {
	env := setupM7(t)
	defer env.close()

	root := newClient(t)
	env.login(t, root, "root@faro.test", "secret123")
	b1 := env.createBranch(t, root, "Centro")

	// Crear repostero CON branchIds => 400.
	if resp := env.do(t, root, http.MethodPost, "/users", map[string]any{
		"email": "bad@vanta.test", "password": "secret123", "name": "Bad", "role": "repostero", "branchIds": []string{b1},
	}); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("crear repostero con sucursal: esperaba 400, obtuvo %d", resp.StatusCode)
	}

	// Crear repostero OK (sin sucursal).
	repoID := env.createRepostero(t, root, "repo@vanta.test")

	// PATCH repostero con branchIds => 400 (blindaje UpdateUser).
	if resp := env.do(t, root, http.MethodPatch, "/users/"+repoID, map[string]any{"branchIds": []string{b1}}); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("asignar sucursal a repostero: esperaba 400, obtuvo %d", resp.StatusCode)
	}
	// PATCH repostero con role => 400 (no se le puede cambiar el rol por esta vía).
	if resp := env.do(t, root, http.MethodPatch, "/users/"+repoID, map[string]any{"role": "cashier"}); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("cambiar rol de repostero: esperaba 400, obtuvo %d", resp.StatusCode)
	}
	// PATCH repostero solo nombre => 200 (no viola el invariante).
	if resp := env.do(t, root, http.MethodPatch, "/users/"+repoID, map[string]any{"name": "Repo Nuevo"}); resp.StatusCode != http.StatusOK {
		t.Fatalf("cambiar nombre de repostero: esperaba 200, obtuvo %d", resp.StatusCode)
	}
	// Sigue sin sucursal.
	var branchCount int
	env.pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM user_branches WHERE user_id=$1`, repoID).Scan(&branchCount)
	if branchCount != 0 {
		t.Fatalf("repostero terminó con %d sucursales (esperaba 0)", branchCount)
	}
}

// ---- Bloqueo de cambio de fulfillment_type (D-C) ---------------------------

// TestFulfillmentTypeChangeBlock verifica la regla D-C: branch_prepared->bakery siempre
// OK; bakery->branch_prepared bloqueado con pedidos abiertos o stock de postre; permitido
// cuando está limpio.
func TestFulfillmentTypeChangeBlock(t *testing.T) {
	env := setupM7(t)
	defer env.close()

	root := newClient(t)
	env.login(t, root, "root@faro.test", "secret123")
	b1 := env.createBranch(t, root, "Centro")

	// branch_prepared -> bakery: siempre permitido (env.productA es branch_prepared).
	if resp := env.do(t, root, http.MethodPatch, "/products/"+env.productA, map[string]any{"fulfillmentType": "bakery"}); resp.StatusCode != http.StatusOK {
		t.Fatalf("branch_prepared->bakery: esperaba 200, obtuvo %d", resp.StatusCode)
	}

	// Con un pedido ABIERTO, bakery -> branch_prepared bloqueado (409).
	env.newBranchUser(t, root, "cajero@vanta.test", "cashier", []string{b1})
	cajero := newClient(t)
	env.login(t, cajero, "cajero@vanta.test", "secret123")
	resp := env.do(t, cajero, http.MethodPost, "/bakery/orders", map[string]any{"productId": env.productA, "quantity": 3})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("crear pedido del postre: esperaba 201, obtuvo %d", resp.StatusCode)
	}
	resp.Body.Close()

	resp = env.do(t, root, http.MethodPatch, "/products/"+env.productA, map[string]any{"fulfillmentType": "branch_prepared"})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("bakery->branch_prepared con pedido abierto: esperaba 409, obtuvo %d", resp.StatusCode)
	}
	var blocked struct {
		Code       string `json:"code"`
		OpenOrders int    `json:"openOrders"`
	}
	decode(t, resp, &blocked)
	if blocked.Code != "fulfillment_change_blocked" || blocked.OpenOrders < 1 {
		t.Fatalf("409 esperado con openOrders>=1, obtuvo %+v", blocked)
	}

	// Cancelar el pedido (queda sin pedidos abiertos y sin stock) -> cambio permitido.
	var orderID string
	env.pool.QueryRow(context.Background(), `SELECT id::text FROM bakery_orders WHERE product_id=$1`, env.productA).Scan(&orderID)
	env.do(t, cajero, http.MethodPatch, "/bakery/orders/"+orderID+"/cancel", nil).Body.Close()

	if resp := env.do(t, root, http.MethodPatch, "/products/"+env.productA, map[string]any{"fulfillmentType": "branch_prepared"}); resp.StatusCode != http.StatusOK {
		t.Fatalf("bakery->branch_prepared limpio: esperaba 200, obtuvo %d", resp.StatusCode)
	}

	// Ahora con STOCK de postre (> 0): reactivar bakery, producir, e intentar el cambio.
	env.do(t, root, http.MethodPatch, "/products/"+env.productA, map[string]any{"fulfillmentType": "bakery"}).Body.Close()
	env.createRepostero(t, root, "repo@vanta.test")
	repo := newClient(t)
	env.login(t, repo, "repo@vanta.test", "secret123")
	resp = env.do(t, cajero, http.MethodPost, "/bakery/orders", map[string]any{"productId": env.productA, "quantity": 2})
	var o struct{ Order struct{ ID string `json:"id"` } `json:"order"` }
	decode(t, resp, &o)
	env.do(t, repo, http.MethodPost, "/bakery/orders/"+o.Order.ID+"/produce", map[string]any{"quantity": 2}).Body.Close()
	// Recibir para cerrar el pedido (queda terminal) pero el stock sigue > 0.
	env.do(t, cajero, http.MethodPatch, "/bakery/orders/"+o.Order.ID+"/receive", nil).Body.Close()

	resp = env.do(t, root, http.MethodPatch, "/products/"+env.productA, map[string]any{"fulfillmentType": "branch_prepared"})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("bakery->branch_prepared con stock>0: esperaba 409, obtuvo %d", resp.StatusCode)
	}
	decode(t, resp, &blocked)
	if blocked.Code != "fulfillment_change_blocked" {
		t.Fatalf("409 esperado por stock, obtuvo %+v", blocked)
	}
}

// ---- Tendencia de venta de postres -----------------------------------------

// TestBakeryTrend verifica la agregación semana vs. semana y el gating por rol.
func TestBakeryTrend(t *testing.T) {
	env := setupM7(t)
	defer env.close()
	ctx := context.Background()

	root := newClient(t)
	env.login(t, root, "root@faro.test", "secret123")
	b1 := env.createBranch(t, root, "Centro")
	cheesecake := env.createBakeryProduct(t, root, "Cheesecake", 8000)

	type weekRange struct {
		From time.Time `json:"from"`
		To   time.Time `json:"to"`
	}
	type trendResp struct {
		WeekCurrent  weekRange `json:"weekCurrent"`
		WeekPrevious weekRange `json:"weekPrevious"`
		Items        []struct {
			ProductName   string   `json:"productName"`
			UnitsPrevious int      `json:"unitsPrevious"`
			UnitsCurrent  int      `json:"unitsCurrent"`
			DeltaUnits    int      `json:"deltaUnits"`
			DeltaPct      *float64 `json:"deltaPct"`
		} `json:"items"`
	}

	// Primer sondeo: leer las ventanas que calcula el backend (semana lunes-lunes) para
	// colocar las ventas dentro de cada una de forma robusta al día de la semana.
	resp := env.do(t, root, http.MethodGet, "/insights/bakery-trend?tz=0", nil)
	var windows trendResp
	decode(t, resp, &windows)

	prevTime := windows.WeekPrevious.From.Add(time.Hour)
	curTime := windows.WeekCurrent.From.Add(time.Hour)
	var saleOld, saleNew string
	env.pool.QueryRow(ctx, `INSERT INTO sales (tenant_id,branch_id,total_cents,amount_paid_cents,change_cents,payment_method,created_at) VALUES ($1,$2,8000,8000,0,'cash',$3) RETURNING id::text`, env.tenantID, b1, prevTime).Scan(&saleOld)
	env.pool.QueryRow(ctx, `INSERT INTO sales (tenant_id,branch_id,total_cents,amount_paid_cents,change_cents,payment_method,created_at) VALUES ($1,$2,14000,14000,0,'cash',$3) RETURNING id::text`, env.tenantID, b1, curTime).Scan(&saleNew)
	env.pool.Exec(ctx, `INSERT INTO sale_items (sale_id,product_id,name,unit_price_cents,quantity,line_total_cents) VALUES ($1,$2,'Cheesecake',8000,8,64000)`, saleOld, cheesecake)
	env.pool.Exec(ctx, `INSERT INTO sale_items (sale_id,product_id,name,unit_price_cents,quantity,line_total_cents) VALUES ($1,$2,'Cheesecake',8000,14,112000)`, saleNew, cheesecake)

	// super_admin ve la tendencia.
	resp = env.do(t, root, http.MethodGet, "/insights/bakery-trend?tz=0", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("super_admin bakery-trend: esperaba 200, obtuvo %d", resp.StatusCode)
	}
	var trend trendResp
	decode(t, resp, &trend)
	if len(trend.Items) != 1 {
		t.Fatalf("bakery-trend: esperaba 1 item, obtuvo %d", len(trend.Items))
	}
	it := trend.Items[0]
	if it.UnitsPrevious != 8 || it.UnitsCurrent != 14 || it.DeltaUnits != 6 || it.DeltaPct == nil || *it.DeltaPct != 75 {
		t.Fatalf("bakery-trend agregación inesperada: %+v (deltaPct=%v)", it, it.DeltaPct)
	}

	// Repostero ve (todas). cashier => 403.
	env.createRepostero(t, root, "repo@vanta.test")
	repo := newClient(t)
	env.login(t, repo, "repo@vanta.test", "secret123")
	if resp := env.do(t, repo, http.MethodGet, "/insights/bakery-trend?tz=0", nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("repostero bakery-trend: esperaba 200, obtuvo %d", resp.StatusCode)
	}
	env.newBranchUser(t, root, "cajero@vanta.test", "cashier", []string{b1})
	cajero := newClient(t)
	env.login(t, cajero, "cajero@vanta.test", "secret123")
	if resp := env.do(t, cajero, http.MethodGet, "/insights/bakery-trend?tz=0", nil); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cashier bakery-trend: esperaba 403, obtuvo %d", resp.StatusCode)
	}
}
