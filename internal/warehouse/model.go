// Package warehouse implementa el módulo de almacén central (M8): un almacén único
// por negocio, intermedio entre "comprar" y "la sucursal consume el insumo". No
// tiene catálogo propio: los ítems del almacén SON los supplies existentes (se
// referencian por FK). Todo el módulo está gated a super_admin.
//
// Dos dominios de stock separados (ADR-008): el almacén (warehouse_stock + ledger
// warehouse_movements) y la sucursal (supply_branch_stock + supply_movements, ya
// existentes). Solo la salida (dispatch) cruza ambos, en una transacción.
//
// Invariante del almacén: para cada supply_id,
// warehouse_stock.stock_base == SUM(warehouse_movements.quantity_base). El cache se
// escribe SIEMPRE en la misma transacción que el movimiento. Nada bloqueante: el
// stock puede quedar negativo (R4).
package warehouse

import "time"

// Supplier es un proveedor del catálogo (tenant-scoped). Baja SOFT vía
// status='inactive' (preserva el historial de compras). Campos opcionales van como
// puntero (nil / JSON null = no capturado).
type Supplier struct {
	ID        string    `json:"id"`
	TenantID  string    `json:"tenantId"`
	Name      string    `json:"name"`
	Address   *string   `json:"address"`
	Email     *string   `json:"email"`
	Phone     *string   `json:"phone"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"createdAt"`
}

// WarehouseStockItem es una fila del listado de existencias del almacén: un supply
// (LEFT JOIN warehouse_stock) con su stock, mín/máx y datos de presentación.
// StockBase es 0 si el supply no tiene fila (lazy). Status es DERIVADO en backend
// (regla del badge): "below_min" si min definido y stock<=min; "no_min" si min es
// null; si no "ok". MinQuantity/MaxQuantity van en unidad base (null = sin límite).
type WarehouseStockItem struct {
	SupplyID         string `json:"supplyId"`
	Name             string `json:"name"`
	BaseUnit         string `json:"baseUnit"`
	PackageName      string `json:"packageName"`
	PackageContent   int    `json:"packageContent"`
	PackageCostCents *int   `json:"packageCostCents"`
	StockBase        int    `json:"stockBase"`
	MinQuantity      *int   `json:"minQuantity"`
	MaxQuantity      *int   `json:"maxQuantity"`
	Status           string `json:"status"` // below_min | ok | no_min
}

// ToBuyItem es un insumo en o bajo mínimo (min definido y stock<=min). Missing =
// min - stock (>= 0), lo que falta para llegar al mínimo.
type ToBuyItem struct {
	SupplyID       string `json:"supplyId"`
	Name           string `json:"name"`
	BaseUnit       string `json:"baseUnit"`
	PackageName    string `json:"packageName"`
	PackageContent int    `json:"packageContent"`
	StockBase      int    `json:"stockBase"`
	MinQuantity    int    `json:"minQuantity"`
	Missing        int    `json:"missing"`
}

// WarehouseMovement es el movimiento recién creado que devuelve un POST (compra,
// salida o merma). quantity_base va FIRMADO: purchase (+), dispatch/waste (−). Los
// campos que no aplican al tipo van nil. Origin distingue, en la merma, desde qué
// ledger se escribió ("warehouse" = almacén; "branch" = sucursal), para que el
// front sepa que stockBase es de almacén o de esa sucursal (§5.5).
type WarehouseMovement struct {
	ID            string    `json:"id"`
	TenantID      string    `json:"tenantId"`
	SupplyID      string    `json:"supplyId"`
	Type          string    `json:"type"`
	QuantityBase  int       `json:"quantityBase"`
	BranchID      *string   `json:"branchId"`
	SupplierID    *string   `json:"supplierId"`
	Packages      *int      `json:"packages"`
	UnitCostCents *int      `json:"unitCostCents"`
	Reason        *string   `json:"reason"`
	Origin        string    `json:"origin,omitempty"` // solo waste: warehouse | branch
	CreatedBy     *string   `json:"createdBy"`
	CreatedAt     time.Time `json:"createdAt"`
}

// PurchaseItem es una fila del historial de compras (F5/F7). TotalCents es DERIVADO
// (packages * unitCostCents). QuantityBase es la entrada en unidad base
// (packages * package_content).
type PurchaseItem struct {
	ID            string    `json:"id"`
	SupplyID      string    `json:"supplyId"`
	SupplyName    string    `json:"supplyName"`
	SupplierID    *string   `json:"supplierId"`
	SupplierName  *string   `json:"supplierName"`
	Packages      *int      `json:"packages"`
	UnitCostCents *int      `json:"unitCostCents"`
	TotalCents    *int      `json:"totalCents"`
	QuantityBase  int       `json:"quantityBase"`
	CreatedByName *string   `json:"createdByName"`
	CreatedAt     time.Time `json:"createdAt"`
}

// DispatchItem es una fila del historial de salidas (F8-F10). QuantityBase es
// NEGATIVO (sale del almacén). BranchName es la sucursal destino.
type DispatchItem struct {
	ID            string    `json:"id"`
	SupplyID      string    `json:"supplyId"`
	SupplyName    string    `json:"supplyName"`
	BranchID      *string   `json:"branchId"`
	BranchName    *string   `json:"branchName"`
	QuantityBase  int       `json:"quantityBase"`
	CreatedByName *string   `json:"createdByName"`
	CreatedAt     time.Time `json:"createdAt"`
}

// WasteItem es una fila del historial de mermas (F11/F12), UNION de las mermas del
// almacén (branch null) y las de sucursal (branch set). QuantityBase es NEGATIVO.
// Origin: "warehouse" (almacén) | "branch" (sucursal). BranchName null => el front
// muestra "—".
type WasteItem struct {
	ID            string    `json:"id"`
	SupplyID      string    `json:"supplyId"`
	SupplyName    string    `json:"supplyName"`
	BranchID      *string   `json:"branchId"`
	BranchName    *string   `json:"branchName"`
	Reason        *string   `json:"reason"`
	QuantityBase  int       `json:"quantityBase"`
	Origin        string    `json:"origin"`
	CreatedByName *string   `json:"createdByName"`
	CreatedAt     time.Time `json:"createdAt"`
}
