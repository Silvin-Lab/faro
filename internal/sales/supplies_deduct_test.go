package sales

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// --- Descuento automático de insumos al vender ---
//
// Estos tests cubren la fase 5 del plan (Gastos/Insumos): al crear una venta se
// descuenta del inventario lo que consume cada producto según su receta global,
// en la MISMA transacción. Se siembran insumos/recetas/stock con SQL directo
// (mismo estilo que el resto del paquete). El TRUNCATE de testSvc borra las
// tablas de insumos por CASCADE (supplies/product_supplies/supply_branch_stock/
// supply_movements referencian tenants/products/branches).

// seedSupply crea un insumo del negocio y devuelve su id.
func seedSupply(t *testing.T, pool *pgxpool.Pool, tenantID, name, baseUnit string, packageContent int) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO supplies (tenant_id, name, base_unit, package_name, package_content)
		 VALUES ($1,$2,$3,$4,$5) RETURNING id::text`,
		tenantID, name, baseUnit, name+" pkg", packageContent).Scan(&id); err != nil {
		t.Fatalf("seed supply: %v", err)
	}
	return id
}

// seedRecipe agrega una línea de receta (producto consume quantityBase del insumo).
func seedRecipe(t *testing.T, pool *pgxpool.Pool, tenantID, productID, supplyID string, quantityBase int) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO product_supplies (tenant_id, product_id, supply_id, quantity_base)
		 VALUES ($1,$2,$3,$4)`, tenantID, productID, supplyID, quantityBase); err != nil {
		t.Fatalf("seed recipe: %v", err)
	}
}

// seedStock inicializa el cache de existencias de un insumo en una sucursal.
func seedStock(t *testing.T, pool *pgxpool.Pool, tenantID, supplyID, branchID string, stockBase int) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO supply_branch_stock (tenant_id, supply_id, branch_id, stock_base)
		 VALUES ($1,$2,$3,$4)`, tenantID, supplyID, branchID, stockBase); err != nil {
		t.Fatalf("seed stock: %v", err)
	}
}

// stockOf lee el stock_base de un insumo en una sucursal (-1 si no hay fila).
func stockOf(t *testing.T, pool *pgxpool.Pool, supplyID, branchID string) int {
	t.Helper()
	var stock int
	err := pool.QueryRow(context.Background(),
		`SELECT stock_base FROM supply_branch_stock WHERE supply_id=$1 AND branch_id=$2`,
		supplyID, branchID).Scan(&stock)
	if err != nil {
		return -1
	}
	return stock
}

// TestSaleDeductsSuppliesPerRecipe: venta de un producto con receta (2 insumos)
// ×3 unidades => 2 movimientos 'sale' con el sale_id y la sucursal de la venta,
// cantidades -(qty×3), stock de ESA sucursal decrementado y la otra intacta.
func TestSaleDeductsSuppliesPerRecipe(t *testing.T) {
	svc, pool, a, _, prodA, _, _, price := testSvc(t)
	defer pool.Close()
	ctx := context.Background()

	branchA := seedBranch(t, pool, a, "Centro")
	branchB := seedBranch(t, pool, a, "Norte")

	milk := seedSupply(t, pool, a, "Leche", "ml", 900)
	coffee := seedSupply(t, pool, a, "Café", "g", 1000)
	// Receta del Latte: 200 ml de leche + 18 g de café por unidad.
	seedRecipe(t, pool, a, prodA, milk, 200)
	seedRecipe(t, pool, a, prodA, coffee, 18)
	// Stock inicial en ambas sucursales; solo A debe cambiar.
	seedStock(t, pool, a, milk, branchA, 5000)
	seedStock(t, pool, a, coffee, branchA, 1000)
	seedStock(t, pool, a, milk, branchB, 5000)
	seedStock(t, pool, a, coffee, branchB, 1000)

	sale, err := svc.Create(ctx, a, []LineInput{{ProductID: prodA, Quantity: 3}}, "cash", price*3, nil, nil, nil, &branchA)
	if err != nil {
		t.Fatalf("crear venta: %v", err)
	}

	// Dos movimientos 'sale', ambos con el sale_id y la sucursal de la venta.
	var nMov int
	pool.QueryRow(ctx,
		`SELECT count(*) FROM supply_movements WHERE sale_id=$1 AND type='sale' AND branch_id=$2`,
		sale.ID, branchA).Scan(&nMov)
	if nMov != 2 {
		t.Fatalf("movimientos 'sale': esperaba 2, obtuvo %d", nMov)
	}

	// Cantidades firmadas: -(200×3) y -(18×3).
	var qMilk, qCoffee int
	pool.QueryRow(ctx, `SELECT quantity_base FROM supply_movements WHERE sale_id=$1 AND supply_id=$2`, sale.ID, milk).Scan(&qMilk)
	pool.QueryRow(ctx, `SELECT quantity_base FROM supply_movements WHERE sale_id=$1 AND supply_id=$2`, sale.ID, coffee).Scan(&qCoffee)
	if qMilk != -600 || qCoffee != -54 {
		t.Fatalf("cantidades: leche=%d café=%d (esperaba -600/-54)", qMilk, qCoffee)
	}

	// created_by NULL y reason NULL en el descuento automático.
	var nWithCreator int
	pool.QueryRow(ctx, `SELECT count(*) FROM supply_movements WHERE sale_id=$1 AND (created_by IS NOT NULL OR reason IS NOT NULL)`, sale.ID).Scan(&nWithCreator)
	if nWithCreator != 0 {
		t.Fatalf("descuento automático: created_by/reason deben ser NULL, filas con dato=%d", nWithCreator)
	}

	// Stock de A decrementado; B intacto.
	if got := stockOf(t, pool, milk, branchA); got != 5000-600 {
		t.Fatalf("stock leche A: esperaba %d, obtuvo %d", 5000-600, got)
	}
	if got := stockOf(t, pool, coffee, branchA); got != 1000-54 {
		t.Fatalf("stock café A: esperaba %d, obtuvo %d", 1000-54, got)
	}
	if got := stockOf(t, pool, milk, branchB); got != 5000 {
		t.Fatalf("stock leche B debía quedar intacto en 5000, obtuvo %d", got)
	}
	if got := stockOf(t, pool, coffee, branchB); got != 1000 {
		t.Fatalf("stock café B debía quedar intacto en 1000, obtuvo %d", got)
	}
}

// TestSaleWithoutRecipeIsNoop (regresión clave): un producto SIN receta se vende
// normal y no genera ningún movimiento de insumos ni fila de stock.
func TestSaleWithoutRecipeIsNoop(t *testing.T) {
	svc, pool, a, _, prodA, _, _, price := testSvc(t)
	defer pool.Close()
	ctx := context.Background()

	branchA := seedBranch(t, pool, a, "Centro")
	// prodA no tiene receta.

	sale, err := svc.Create(ctx, a, []LineInput{{ProductID: prodA, Quantity: 2}}, "cash", price*2, nil, nil, nil, &branchA)
	if err != nil {
		t.Fatalf("venta sin receta: %v", err)
	}
	if sale.TotalCents != price*2 {
		t.Fatalf("total: esperaba %d, obtuvo %d", price*2, sale.TotalCents)
	}

	var nMov, nStock int
	pool.QueryRow(ctx, `SELECT count(*) FROM supply_movements WHERE sale_id=$1`, sale.ID).Scan(&nMov)
	pool.QueryRow(ctx, `SELECT count(*) FROM supply_branch_stock WHERE tenant_id=$1`, a).Scan(&nStock)
	if nMov != 0 {
		t.Fatalf("producto sin receta: esperaba 0 movimientos, obtuvo %d", nMov)
	}
	if nStock != 0 {
		t.Fatalf("producto sin receta: no debía tocar supply_branch_stock, filas=%d", nStock)
	}
}

// TestSaleNeverBlocksOnStockGoesNegative: stock 100, la receta consume 500 ⇒ la
// venta se registra igual y el stock queda en -400 (nada bloquea la venta).
func TestSaleNeverBlocksOnStockGoesNegative(t *testing.T) {
	svc, pool, a, _, prodA, _, _, price := testSvc(t)
	defer pool.Close()
	ctx := context.Background()

	branchA := seedBranch(t, pool, a, "Centro")
	sugar := seedSupply(t, pool, a, "Azúcar", "g", 1000)
	seedRecipe(t, pool, a, prodA, sugar, 500)
	seedStock(t, pool, a, sugar, branchA, 100)

	sale, err := svc.Create(ctx, a, []LineInput{{ProductID: prodA, Quantity: 1}}, "cash", price, nil, nil, nil, &branchA)
	if err != nil {
		t.Fatalf("venta sobre stock insuficiente debía pasar: %v", err)
	}
	if got := stockOf(t, pool, sugar, branchA); got != -400 {
		t.Fatalf("stock negativo: esperaba -400, obtuvo %d", got)
	}
	var q int
	pool.QueryRow(ctx, `SELECT quantity_base FROM supply_movements WHERE sale_id=$1 AND supply_id=$2`, sale.ID, sugar).Scan(&q)
	if q != -500 {
		t.Fatalf("movimiento: esperaba -500, obtuvo %d", q)
	}
}

// TestSaleMixedCartAggregatesSameProduct: carrito con un producto con receta
// (repetido en dos líneas) y otro sin receta. El consumo del producto con receta
// se agrega entre líneas (qty total) y el producto sin receta no aporta filas.
func TestSaleMixedCartAggregatesSameProduct(t *testing.T) {
	svc, pool, a, _, prodA, _, _, price := testSvc(t)
	defer pool.Close()
	ctx := context.Background()

	branchA := seedBranch(t, pool, a, "Centro")

	// Producto sin receta (además de prodA con receta).
	var prodNoRecipe string
	pool.QueryRow(ctx, "INSERT INTO products (tenant_id, name, price_cents) VALUES ($1,'Galleta',1500) RETURNING id::text", a).Scan(&prodNoRecipe)

	milk := seedSupply(t, pool, a, "Leche", "ml", 900)
	seedRecipe(t, pool, a, prodA, milk, 200)
	seedStock(t, pool, a, milk, branchA, 5000)

	// prodA aparece en DOS líneas (2 + 3 = 5 unidades) + un producto sin receta.
	sale, err := svc.Create(ctx, a, []LineInput{
		{ProductID: prodA, Quantity: 2},
		{ProductID: prodNoRecipe, Quantity: 4},
		{ProductID: prodA, Quantity: 3},
	}, "cash", price*5+1500*4, nil, nil, nil, &branchA)
	if err != nil {
		t.Fatalf("venta mixta: %v", err)
	}

	// Un solo movimiento de leche, agregando las dos líneas de prodA: -(200×5).
	var nMov, q int
	pool.QueryRow(ctx, `SELECT count(*), coalesce(sum(quantity_base),0) FROM supply_movements WHERE sale_id=$1`, sale.ID).Scan(&nMov, &q)
	if nMov != 1 || q != -1000 {
		t.Fatalf("agregación: esperaba 1 movimiento de -1000, obtuvo n=%d suma=%d", nMov, q)
	}
	if got := stockOf(t, pool, milk, branchA); got != 5000-1000 {
		t.Fatalf("stock leche: esperaba %d, obtuvo %d", 5000-1000, got)
	}
}

// TestSaleRollsBackDeductionOnLaterFailure: si algo falla DESPUÉS del descuento
// (aquí, la actualización del contador de lealtad del cliente, que ocurre tras
// deductSupplies), la transacción entera revierte: cero movimientos, stock
// intacto y ninguna venta registrada. Se fuerza el fallo con un trigger temporal
// sobre customers (se elimina al final).
func TestSaleRollsBackDeductionOnLaterFailure(t *testing.T) {
	svc, pool, a, _, prodA, _, _, price := testSvc(t)
	defer pool.Close()
	ctx := context.Background()

	branchA := seedBranch(t, pool, a, "Centro")
	milk := seedSupply(t, pool, a, "Leche", "ml", 900)
	seedRecipe(t, pool, a, prodA, milk, 200)
	seedStock(t, pool, a, milk, branchA, 5000)

	var cust string
	pool.QueryRow(ctx, "INSERT INTO customers (tenant_id, phone, first_name, last_name, visits) VALUES ($1,'555','Ana','Paz',0) RETURNING id::text", a).Scan(&cust)

	// Trigger que revienta el UPDATE de customers (paso posterior al descuento en
	// createSale). Se limpia con defer para no filtrar estado a otros tests.
	if _, err := pool.Exec(ctx,
		`CREATE OR REPLACE FUNCTION faro_test_fail_customer_update() RETURNS trigger AS $$
		 BEGIN RAISE EXCEPTION 'boom en update de customers'; END; $$ LANGUAGE plpgsql`); err != nil {
		t.Fatalf("crear función trigger: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`CREATE TRIGGER faro_test_boom BEFORE UPDATE ON customers
		   FOR EACH ROW EXECUTE FUNCTION faro_test_fail_customer_update()`); err != nil {
		t.Fatalf("crear trigger: %v", err)
	}
	defer func() {
		pool.Exec(ctx, `DROP TRIGGER IF EXISTS faro_test_boom ON customers`)
		pool.Exec(ctx, `DROP FUNCTION IF EXISTS faro_test_fail_customer_update()`)
	}()

	// La venta con cliente llega hasta el UPDATE de customers y falla ahí.
	if _, err := svc.Create(ctx, a, []LineInput{{ProductID: prodA, Quantity: 3}}, "cash", price*3, &cust, nil, nil, &branchA); err == nil {
		t.Fatalf("esperaba error por el trigger, la venta no debía completarse")
	}

	// Rollback total: sin venta, sin movimientos, stock intacto.
	var nSales, nMov int
	pool.QueryRow(ctx, `SELECT count(*) FROM sales WHERE tenant_id=$1`, a).Scan(&nSales)
	pool.QueryRow(ctx, `SELECT count(*) FROM supply_movements WHERE tenant_id=$1`, a).Scan(&nMov)
	if nSales != 0 {
		t.Fatalf("rollback: esperaba 0 ventas, obtuvo %d", nSales)
	}
	if nMov != 0 {
		t.Fatalf("rollback: esperaba 0 movimientos de insumos, obtuvo %d", nMov)
	}
	if got := stockOf(t, pool, milk, branchA); got != 5000 {
		t.Fatalf("rollback: stock debía quedar intacto en 5000, obtuvo %d", got)
	}
}
