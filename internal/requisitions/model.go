// Package requisitions implementa el módulo de requisiciones de insumos (M11): una
// sucursal solicita a la matriz los insumos que le faltan; la solicitud entra en una cola
// FIFO visible para quien la resuelve (super_admin), y se surte vía salidas del almacén
// (warehouse dispatch) que actualizan lo despachado por línea y el estado de la requisición.
//
// Réplica del patrón de bakery_orders/bakery_productions pero sobre insumos: header
// (supply_requisitions) + líneas (supply_requisition_items). El surtido NO vive aquí: lo
// hace el módulo warehouse en su transacción de dispatch (warehouse_movements.requisition_id
// liga la traza). Este módulo solo crea, lista, cancela, cierra y sugiere.
//
// Cantidades en UNIDAD BASE (mismo criterio que almacén/insumos). Autorización inline por
// rol en cada handler: sucursal (crea/ve la suya), super_admin (ve todas, cierra).
package requisitions

import "time"

// Requisition es el encabezado de una requisición, con nombres resueltos.
type Requisition struct {
	ID              string    `json:"id"`
	BranchID        string    `json:"branchId"`
	BranchName      string    `json:"branchName"`
	Status          string    `json:"status"`
	Note            *string   `json:"note"`
	RequestedByName *string   `json:"requestedByName"`
	CreatedAt       time.Time `json:"createdAt"`
	UpdatedAt       time.Time `json:"updatedAt"`
}

// RequisitionItem es una línea de una requisición: insumo, cantidad solicitada y cuánto se
// ha surtido (acumulado por los dispatches ligados).
type RequisitionItem struct {
	ID                string  `json:"id"`
	SupplyID          string  `json:"supplyId"`
	SupplyName        string  `json:"supplyName"`
	BaseUnit          string  `json:"baseUnit"`
	QuantityRequested int     `json:"quantityRequested"`
	QuantityFulfilled int     `json:"quantityFulfilled"`
	Note              *string `json:"note"`
}

// Movement es un dispatch del almacén ligado a la requisición (traza de lo surtido).
type Movement struct {
	ID            string    `json:"id"`
	SupplyID      string    `json:"supplyId"`
	SupplyName    string    `json:"supplyName"`
	QuantityBase  int       `json:"quantityBase"`
	CreatedByName *string   `json:"createdByName"`
	CreatedAt     time.Time `json:"createdAt"`
}

// RequisitionDetail es una requisición con su encabezado, líneas y movimientos ligados.
type RequisitionDetail struct {
	Requisition Requisition       `json:"requisition"`
	Items       []RequisitionItem `json:"items"`
	Movements   []Movement        `json:"movements"`
}

// Suggestion es una sugerencia de reposición: un insumo de la sucursal bajo su mínimo, con
// la cantidad sugerida a pedir. Puramente informativo.
type Suggestion struct {
	SupplyID     string `json:"supplyId"`
	SupplyName   string `json:"supplyName"`
	BaseUnit     string `json:"baseUnit"`
	StockBase    int    `json:"stockBase"`
	MinQuantity  int    `json:"minQuantity"`
	MaxQuantity  *int   `json:"maxQuantity"`
	SuggestedQty int    `json:"suggestedQty"`
}

// LineInput es una línea de entrada al crear una requisición.
type LineInput struct {
	SupplyID     string
	QuantityBase int
	Note         *string
}
