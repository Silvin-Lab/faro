// Package loyalty gestiona el programa de lealtad por promociones de un negocio
// (CRUD de promociones) y su integración con clientes y ventas: elegibilidad de
// clientes e historial de canjes. Reemplaza la config única de la v1.
package loyalty

import "time"

// Promotion es una promoción de lealtad de un negocio: un umbral de visitas que,
// al alcanzarse, otorga un descuento (o producto gratis con 100%) sobre una
// unidad de alguno de sus productos elegibles.
type Promotion struct {
	ID              string    `json:"id"`
	Name            string    `json:"name"`
	DiscountPercent int       `json:"discountPercent"` // 1..100 (100 = gratis)
	VisitThreshold  int       `json:"visitThreshold"`  // umbral de visitas (> 0)
	ResetsCounter   bool      `json:"resetsCounter"`   // al aplicar: visits -> 0 + snapshot
	Status          string    `json:"status"`          // active | inactive (archivada)
	ProductIDs      []string  `json:"productIds"`
	CreatedAt       time.Time `json:"createdAt"`
	UpdatedAt       time.Time `json:"updatedAt"`
}

// PromotionInput son los datos editables de una promoción (POST/PUT).
type PromotionInput struct {
	Name            string
	DiscountPercent int
	VisitThreshold  int
	ResetsCounter   bool
	ProductIDs      []string
}

// CustomerStatus resume la lealtad de un cliente para el POS: visitas y, por cada
// promoción activa, cuántas faltan y qué productos aplican.
type CustomerStatus struct {
	CustomerID     string            `json:"customerId"`
	Visits         int               `json:"visits"`
	VisitsLifetime int               `json:"visitsLifetime"`
	Promotions     []PromotionStatus `json:"promotions"`
}

// PromotionStatus es la elegibilidad de una promoción para un cliente concreto.
type PromotionStatus struct {
	PromotionID       string         `json:"promotionId"`
	Name              string         `json:"name"`
	DiscountPercent   int            `json:"discountPercent"`
	VisitThreshold    int            `json:"visitThreshold"`
	ResetsCounter     bool           `json:"resetsCounter"`
	VisitsRemaining   int            `json:"visitsRemaining"`   // max(0, threshold - visits)
	RedeemedThisCycle bool           `json:"redeemedThisCycle"` // ya canjeada en el ciclo actual
	ApplicableNow     bool           `json:"applicableNow"`     // (visits+1 >= threshold) AND NOT redeemedThisCycle
	Products          []PromoProduct `json:"products"`
}

// PromoProduct es un producto elegible de una promoción (para mostrar y elegir la
// unidad beneficiada en el POS).
type PromoProduct struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	PriceCents int    `json:"priceCents"`
}
