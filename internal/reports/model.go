// Package reports produce reportes agregados de ventas (solo lectura), acotados
// por negocio y rango de fechas.
package reports

type PaymentBreakdown struct {
	Method     string `json:"method"`
	Count      int    `json:"count"`
	TotalCents int    `json:"totalCents"`
}

type CategoryBreakdown struct {
	CategoryName string `json:"categoryName"`
	Quantity     int    `json:"quantity"`
	TotalCents   int    `json:"totalCents"`
}

// ProductBreakdown agrega ventas por producto dentro de su categoría (resumen al
// final del reporte: qué productos y cuántos de cada uno se vendieron). Viene
// ordenado por categoría y luego por cantidad vendida descendente; el frontend lo
// agrupa por CategoryName para el desglose.
type ProductBreakdown struct {
	CategoryName string `json:"categoryName"`
	ProductName  string `json:"productName"`
	Quantity     int    `json:"quantity"`
	TotalCents   int    `json:"totalCents"`
}

type HourBreakdown struct {
	Hour       int `json:"hour"`
	Count      int `json:"count"`
	TotalCents int `json:"totalCents"`
}

// BranchBreakdown agrega ventas por sucursal. BranchID nil = bucket "Sin sucursal".
type BranchBreakdown struct {
	BranchID   *string `json:"branchId"`
	BranchName string  `json:"branchName"`
	TotalCents int     `json:"totalCents"`
	SalesCount int     `json:"salesCount"`
}

// BranchFilter acota el reporte a una sucursal. All = sin filtro; None = ventas
// sin sucursal (branch_id IS NULL); ID = una sucursal concreta.
type BranchFilter struct {
	None bool
	ID   *string
}

// SaleListItem es una fila del listado de ventas individuales del reporte
// (sección "Historial de ventas", acotada a rangos ≤48h — ver handler). El
// detalle completo (items) se obtiene aparte con GET /sales/{id}.
type SaleListItem struct {
	ID            string  `json:"id"`
	CreatedAt     string  `json:"createdAt"`
	CustomerName  *string `json:"customerName"`
	TotalCents    int     `json:"totalCents"`
	PaymentMethod string  `json:"paymentMethod"`
	// Descuento de convenio (M12). Aditivos: ventas sin convenio => 0/null.
	AgreementDiscountPercent *int    `json:"agreementDiscountPercent"`
	AgreementDiscountCents   int     `json:"agreementDiscountCents"`
	SoldByName               *string `json:"soldByName"` // null => "Sin registro"
}

// AgreementDiscountPercentBreakdown desglosa los descuentos de convenio por %.
type AgreementDiscountPercentBreakdown struct {
	Percent    int `json:"percent"`
	Count      int `json:"count"`
	TotalCents int `json:"totalCents"`
}

// AgreementDiscountsSummary resume los descuentos de convenio del período. El
// total de ventas del reporte sigue siendo NETO (consistente con lealtad); este
// bloque expone el monto de convenio aislado (ADR-010 §D1).
type AgreementDiscountsSummary struct {
	SalesCount int                                 `json:"salesCount"` // ventas con convenio
	TotalCents int                                 `json:"totalCents"` // suma de agreement_discount_cents
	ByPercent  []AgreementDiscountPercentBreakdown `json:"byPercent"`
}

type SalesReport struct {
	TotalCents         int                       `json:"totalCents"`
	SalesCount         int                       `json:"salesCount"`
	ByPaymentMethod    []PaymentBreakdown        `json:"byPaymentMethod"`
	ByCategory         []CategoryBreakdown       `json:"byCategory"`
	ByHour             []HourBreakdown           `json:"byHour"`
	ByBranch           []BranchBreakdown         `json:"byBranch"`
	ByProduct          []ProductBreakdown        `json:"byProduct"`
	AgreementDiscounts AgreementDiscountsSummary `json:"agreementDiscounts"`
}

// ---- Reporte de gastos -----------------------------------------------------

// ExpensesSummary es el resumen del reporte de gastos.
type ExpensesSummary struct {
	ExpensesCount int `json:"expensesCount"`
	TotalCents    int `json:"totalCents"`
}

// ExpenseCategoryBreakdown agrega gastos por categoría de gasto. El bucket sin
// categoría (concepto sin categoría o borrado) se etiqueta "Sin categoría".
type ExpenseCategoryBreakdown struct {
	CategoryName string `json:"categoryName"`
	Count        int    `json:"count"`
	TotalCents   int    `json:"totalCents"`
}

// ExpenseBranchBreakdown agrega gastos por sucursal.
type ExpenseBranchBreakdown struct {
	BranchID   *string `json:"branchId"`
	BranchName string  `json:"branchName"`
	Count      int     `json:"count"`
	TotalCents int     `json:"totalCents"`
}

type ExpensesReport struct {
	Summary    ExpensesSummary            `json:"summary"`
	ByCategory []ExpenseCategoryBreakdown `json:"byCategory"`
	ByBranch   []ExpenseBranchBreakdown   `json:"byBranch"`
}
