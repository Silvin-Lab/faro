// Package sales implementa el punto de venta: registra ventas con sus líneas,
// calculando el total en el servidor (precios propios) y el cambio.
package sales

import "time"

// SaleItem es una línea de venta (snapshot de nombre y precio al momento).
type SaleItem struct {
	ID             string  `json:"id"`
	ProductID      *string `json:"productId"`
	Name           string  `json:"name"`
	UnitPriceCents int     `json:"unitPriceCents"`
	Quantity       int     `json:"quantity"`
	LineTotalCents int     `json:"lineTotalCents"`
}

// Sale es una venta registrada.
type Sale struct {
	ID              string     `json:"id"`
	TenantID        string     `json:"tenantId"`
	TotalCents      int        `json:"totalCents"`
	AmountPaidCents int        `json:"amountPaidCents"`
	ChangeCents     int        `json:"changeCents"`
	DiscountCents   int        `json:"discountCents"` // descuento de lealtad calculado en servidor
	PromotionName   *string    `json:"promotionName"` // nombre de la promo aplicada (o null)
	PaymentMethod   string     `json:"paymentMethod"` // cash | card | transfer | didi
	CustomerID      *string    `json:"customerId"`
	CustomerName    *string    `json:"customerName"`
	BranchID        *string    `json:"branchId"`   // sucursal derivada del usuario (nullable)
	BranchName      *string    `json:"branchName"` // nombre de la sucursal (o null)
	CreatedAt       time.Time  `json:"createdAt"`
	Items           []SaleItem `json:"items,omitempty"`
}
