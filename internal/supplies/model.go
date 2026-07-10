// Package supplies implementa el módulo de insumos: catálogo de insumos
// (administrado por super admin), recetas globales por producto e inventario POR
// SUCURSAL con un ledger de movimientos (fuente de verdad) y un cache de
// existencias. Reutiliza los patrones de categories (CRUD tenant-scoped), sales
// (sucursal) y reports (rango [from,to)). Ver plan Gastos/Insumos.
//
// Invariante del inventario: para cada (supply_id, branch_id),
// supply_branch_stock.stock_base == SUM(supply_movements.quantity_base). El cache
// se actualiza SIEMPRE en la misma transacción que el movimiento. Nada es
// bloqueante: el stock puede quedar negativo (merma/cortesía o venta sin
// existencias). Query de recomputación documentada en 0015_supplies.up.sql.
package supplies

import "time"

// Supply es un insumo del catálogo. base_unit es INMUTABLE tras la creación.
// package_content = contenido de UNA presentación en unidad base. Stock se llena
// solo en el listado (existencias por sucursal); en create/get/update va como
// lista vacía — el contrato promete siempre un array, nunca null/ausente.
//
// PackageCostCents = costo de UNA presentación en centavos (ej. "Bote 900 ml" a
// $85.00 -> 8500). Puntero: nil / JSON null = costo no capturado (desconocido); no
// se usa 0 como "desconocido" (0 sería un costo real de cero).
type Supply struct {
	ID               string        `json:"id"`
	TenantID         string        `json:"tenantId"`
	Name             string        `json:"name"`
	BaseUnit         string        `json:"baseUnit"` // g | ml | pieza
	PackageName      string        `json:"packageName"`
	PackageContent   int           `json:"packageContent"`
	PackageCostCents *int          `json:"packageCostCents"` // centavos; null = desconocido
	Status           string        `json:"status"`
	CreatedAt        time.Time     `json:"createdAt"`
	Stock            []BranchStock `json:"stock"`
}

// BranchStock son las existencias de un insumo en una sucursal (cache). stockBase
// puede ser negativo. Solo aparecen las sucursales con al menos un movimiento (una
// fila en supply_branch_stock); la UI trata una sucursal ausente como stock 0.
type BranchStock struct {
	BranchID   string `json:"branchId"`
	BranchName string `json:"branchName"`
	StockBase  int    `json:"stockBase"`
}

// Movement es un movimiento del ledger. quantity_base va FIRMADO (+ entra, − sale).
// type: purchase | adjustment | sale (sale lo escribe el descuento automático en
// una fase posterior; created_by NULL => venta automática).
type Movement struct {
	ID            string    `json:"id"`
	TenantID      string    `json:"tenantId"`
	SupplyID      string    `json:"supplyId"`
	BranchID      string    `json:"branchId"`
	BranchName    *string   `json:"branchName"`
	Type          string    `json:"type"`
	QuantityBase  int       `json:"quantityBase"`
	Reason        *string   `json:"reason"`
	SaleID        *string   `json:"saleId"`
	CreatedBy     *string   `json:"createdBy"`
	CreatedByName *string   `json:"createdByName"`
	CreatedAt     time.Time `json:"createdAt"`
}

// RecipeItem es una línea de receta (insumo + cantidad por unidad vendida). La
// receta es global (no depende de sucursal).
type RecipeItem struct {
	SupplyID     string `json:"supplyId"`
	SupplyName   string `json:"supplyName"`
	BaseUnit     string `json:"baseUnit"`
	QuantityBase int    `json:"quantityBase"`
}
