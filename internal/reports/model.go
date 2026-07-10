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

type SalesReport struct {
	TotalCents      int                 `json:"totalCents"`
	SalesCount      int                 `json:"salesCount"`
	ByPaymentMethod []PaymentBreakdown  `json:"byPaymentMethod"`
	ByCategory      []CategoryBreakdown `json:"byCategory"`
	ByHour          []HourBreakdown     `json:"byHour"`
	ByBranch        []BranchBreakdown   `json:"byBranch"`
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
