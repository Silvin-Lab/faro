// Package expenses implementa el módulo de gastos: catálogo de categorías y
// conceptos (administrado por super admin) y el registro de gastos por sucursal.
// Reutiliza los patrones de categories (CRUD tenant-scoped), sales (sucursal del
// claim) y reports (rango [from,to)). Ver plan Gastos/Insumos.
package expenses

import "time"

// Category es una categoría de gasto (espejo de categories sin imagen).
type Category struct {
	ID        string    `json:"id"`
	TenantID  string    `json:"tenantId"`
	Name      string    `json:"name"`
	Status    string    `json:"status"`
	SortOrder int       `json:"sortOrder"`
	CreatedAt time.Time `json:"createdAt"`
}

// Concept es un concepto de gasto, opcionalmente agrupado en una categoría.
// CategoryID/CategoryName nil => "Sin categoría".
type Concept struct {
	ID           string    `json:"id"`
	TenantID     string    `json:"tenantId"`
	CategoryID   *string   `json:"categoryId"`
	CategoryName *string   `json:"categoryName"`
	Name         string    `json:"name"`
	Status       string    `json:"status"`
	CreatedAt    time.Time `json:"createdAt"`
}

// Expense es un gasto registrado. concept_name es un snapshot (patrón
// sale_items.name): sobrevive a cambios/borrado del concepto.
type Expense struct {
	ID            string    `json:"id"`
	TenantID      string    `json:"tenantId"`
	BranchID      string    `json:"branchId"`
	BranchName    *string   `json:"branchName"`
	ConceptID     *string   `json:"conceptId"`
	ConceptName   string    `json:"conceptName"`
	CategoryName  *string   `json:"categoryName"`
	AmountCents   int       `json:"amountCents"`
	CreatedBy     *string   `json:"createdBy"`
	CreatedByName *string   `json:"createdByName"`
	CreatedAt     time.Time `json:"createdAt"`
}
