// Package customers gestiona los clientes del programa de lealtad (alta y
// búsqueda por teléfono), acotados por negocio. Es la puerta de entrada para
// asociar ventas a un cliente.
package customers

import "time"

type Customer struct {
	ID             string    `json:"id"`
	TenantID       string    `json:"tenantId"`
	Phone          string    `json:"phone"`
	FirstName      string    `json:"firstName"`
	LastName       string    `json:"lastName"`
	Visits         int       `json:"visits"`         // visitas del ciclo actual (lealtad)
	VisitsLifetime int       `json:"visitsLifetime"` // acumulado de por vida (nunca reinicia)
	CreatedAt      time.Time `json:"createdAt"`
}
