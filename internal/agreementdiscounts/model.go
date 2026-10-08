// Package agreementdiscounts gestiona el catálogo de descuentos de convenio de un
// negocio (un % sobre toda la compra). CRUD para el admin (super admin) y lectura
// desde el POS para cualquier sesión. Patrón tomado de loyalty (ADR-005/010).
package agreementdiscounts

import "time"

// AgreementDiscount es un descuento de convenio del catálogo de un negocio.
type AgreementDiscount struct {
	ID        string    `json:"id"`
	Percent   int       `json:"percent"` // 1..100
	Status    string    `json:"status"`  // active | inactive (archivada)
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}
