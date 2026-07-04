// Package branches gestiona las sucursales de un negocio (tenant-scoped): CRUD de
// sucursales con nombre único por negocio y ciclo de vida active|inactive. La
// sucursal se asigna a usuarios (auth) y se deriva a cada venta (sales). Ver ADR-006.
package branches

import "time"

// Branch es una sucursal física de un negocio.
type Branch struct {
	ID        string    `json:"id"`
	TenantID  string    `json:"tenantId"`
	Name      string    `json:"name"`
	Status    string    `json:"status"` // active | inactive (archivada)
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// BranchPatch son los cambios opcionales de una sucursal (PATCH). Un puntero nil
// significa "no cambiar ese campo".
type BranchPatch struct {
	Name   *string
	Status *string
}
