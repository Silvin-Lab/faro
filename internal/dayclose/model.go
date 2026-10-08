// Package dayclose implementa el corte de caja / cierre de día por sucursal (M11):
// sistematiza lo que hoy Vanta hace a mano por WhatsApp/papel al cierre (corte de caja,
// conteo de postres, insumos faltantes). Un cierre por (sucursal, fecha).
//
// Ciclo draft -> submitted: en draft los totales (ventas, gastos, efectivo esperado) se
// calculan EN VIVO llamando directamente al módulo reports (sin HTTP); al submit se CONGELA
// el snapshot y ya no cambia. Liga opcionalmente el conteo de postres (bakery_count_id) y
// la requisición de insumos faltantes (supply_requisition_id) del día.
//
// Gating PROPIO (no reutiliza el de /reports, que es 403 para cashier/barista): el cierre
// lo hace el equipo de piso, así que cualquier rol de sucursal opera el de SU sucursal
// activa; super_admin ve todos.
package dayclose

import "time"

// Closure es un cierre de día de una sucursal. Los montos van en centavos. En estado draft
// los totales de la respuesta son EN VIVO (no persistidos); en submitted son el snapshot
// congelado.
type Closure struct {
	ID                  string     `json:"id"`
	BranchID            string     `json:"branchId"`
	BranchName          string     `json:"branchName"`
	ClosureDate         string     `json:"closureDate"`
	Status              string     `json:"status"`
	TotalSalesCents     *int       `json:"totalSalesCents"`
	TotalExpensesCents  *int       `json:"totalExpensesCents"`
	CashExpectedCents   *int       `json:"cashExpectedCents"`
	CashCountedCents    *int       `json:"cashCountedCents"`
	CashDiffCents       *int       `json:"cashDiffCents"`
	BakeryCountID       *string    `json:"bakeryCountId"`
	SupplyRequisitionID *string    `json:"supplyRequisitionId"`
	Notes               *string    `json:"notes"`
	CreatedByName       *string    `json:"createdByName"`
	SubmittedByName     *string    `json:"submittedByName"`
	SubmittedAt         *time.Time `json:"submittedAt"`
	CreatedAt           time.Time  `json:"createdAt"`
	UpdatedAt           time.Time  `json:"updatedAt"`
}

// DraftPatch son los campos editables de un cierre en borrador (nil = no cambia).
type DraftPatch struct {
	CashCountedCents    *int
	Notes               *string
	BakeryCountID       *string
	SupplyRequisitionID *string
}
