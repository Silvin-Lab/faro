// Package auth implementa la autenticación de Faro: usuarios, negocios (tenants),
// login con email+password, sesión por JWT en cookie httpOnly y scoping por tenant.
package auth

import "time"

// Tenant representa un negocio (cafetería). Aísla los datos de cada cliente.
type Tenant struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"createdAt"`
}

// User representa a un usuario del sistema. password_hash nunca se expone.
type User struct {
	ID           string      `json:"id"`
	TenantID     *string     `json:"tenantId"` // nil solo para el super admin global
	Email        string      `json:"email"`
	Name         string      `json:"name"`
	Role         string      `json:"role"` // super_admin | branch_admin | cashier | barista
	IsSuperAdmin bool        `json:"isSuperAdmin"`
	Status       string      `json:"status"`
	Branches     []BranchRef `json:"branches,omitempty"` // membresías M:N (endpoints de /users)
	CreatedAt    time.Time   `json:"createdAt"`
}

// BranchRef es la referencia mínima de una sucursal expuesta al cliente.
type BranchRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// MeTenant es el negocio expuesto en GET /auth/me (para favicon y marca). null
// cuando el usuario no tiene tenant (super admin global).
type MeTenant struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	FaviconURL *string `json:"faviconUrl"`
}
