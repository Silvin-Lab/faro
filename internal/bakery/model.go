// Package bakery implementa el módulo de repostería / producción central (M10): pedidos
// de sucursal a la repostería, registro de producción (doble efecto sobre el almacén y el
// stock de postre por sucursal), stock de producto terminado y auditoría de producciones.
//
// Reusa el almacén central único (warehouse_stock/warehouse_movements, ADR-008) para el
// consumo de insumos y las recetas existentes (product_supplies) reinterpretadas como
// "consumo por unidad producida" cuando products.fulfillment_type='bakery' (ADR-010 D2).
// Introduce un TERCER dominio de stock: producto terminado por sucursal
// (product_branch_stock + product_stock_movements, ADR-010 D1), con la misma invariante
// cache == SUM(ledger) escrita siempre en la misma transacción.
//
// Autorización inline por rol en cada handler (5 roles con permisos distintos): sucursal
// (branch_admin/cashier/barista), repostero y super_admin.
package bakery

import "time"

// Order es un pedido de una sucursal a la repostería, con nombres resueltos (branch,
// producto, solicitante) para el cliente. QuantityShipped es el acumulado despachado por
// las producciones; "Falta" = QuantityOrdered − QuantityShipped lo calcula el front.
type Order struct {
	ID              string    `json:"id"`
	BranchID        string    `json:"branchId"`
	BranchName      string    `json:"branchName"`
	ProductID       string    `json:"productId"`
	ProductName     string    `json:"productName"`
	QuantityOrdered int       `json:"quantityOrdered"`
	QuantityShipped int       `json:"quantityShipped"`
	Status          string    `json:"status"`
	Note            *string   `json:"note"`
	RequestedByName *string   `json:"requestedByName"`
	CreatedAt       time.Time `json:"createdAt"`
}

// Production es una producción registrada contra un pedido (vista de detalle §5.3 y
// respuesta de produce §5.5). CreatedByName es null en la respuesta de produce.
type Production struct {
	ID               string    `json:"id"`
	QuantityProduced int       `json:"quantityProduced"`
	CreatedByName    *string   `json:"createdByName,omitempty"`
	CreatedAt        time.Time `json:"createdAt"`
}

// ProductionAudit es una fila de la auditoría de producciones (§5.8): incluye pedido,
// postre y sucursal resueltos.
type ProductionAudit struct {
	ID               string    `json:"id"`
	OrderID          string    `json:"orderId"`
	ProductName      string    `json:"productName"`
	BranchName       string    `json:"branchName"`
	QuantityProduced int       `json:"quantityProduced"`
	CreatedByName    *string   `json:"createdByName"`
	CreatedAt        time.Time `json:"createdAt"`
}

// StockItem es una fila del stock de postres: un renglón por (producto, sucursal) (§3.4
// D-D). StockQty puede ser 0 o negativo (D-B).
type StockItem struct {
	ProductID   string `json:"productId"`
	ProductName string `json:"productName"`
	BranchID    string `json:"branchId"`
	BranchName  string `json:"branchName"`
	StockQty    int    `json:"stockQty"`
}
