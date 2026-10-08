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
	CreatedByName  *string   `json:"createdByName"`  // quién registró al cliente (null => histórico/"Sin registro")
	CreatedAt      time.Time `json:"createdAt"`
}

// VisitChange es un evento del historial de cambios de visitas de un cliente
// (auditoría): quién lo hizo, de cuánto a cuánto y con qué origen.
type VisitChange struct {
	ID           string    `json:"id"`
	VisitsBefore int       `json:"visitsBefore"`
	VisitsAfter  int       `json:"visitsAfter"`
	Source       string    `json:"source"`   // create | adjust
	ByUserID     *string   `json:"byUserId"` // usuario de la sesión (o null)
	ByName       *string   `json:"byName"`   // nombre del usuario (null => "Sin registro")
	CreatedAt    time.Time `json:"createdAt"`
}
