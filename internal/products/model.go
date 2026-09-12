// Package products implementa el CRUD del catálogo de productos por negocio,
// reutilizando la sesión/tenant-scope de auth y las categorías (M2).
package products

import "time"

// Product es un producto del catálogo de un negocio. price_cents es entero.
// FulfillmentType (M10): "branch_prepared" (default, receta consumida al vender) o
// "bakery" (surtido por la repostería central; receta consumida al producir, stock
// terminado propio por sucursal). Ver ADR-010 D2.
type Product struct {
	ID              string    `json:"id"`
	TenantID        string    `json:"tenantId"`
	CategoryID      *string   `json:"categoryId"`
	CategoryName    *string   `json:"categoryName"`
	Name            string    `json:"name"`
	PriceCents      int       `json:"priceCents"`
	Status          string    `json:"status"`
	ImageURL        *string   `json:"imageUrl"`
	FulfillmentType string    `json:"fulfillmentType"`
	CreatedAt       time.Time `json:"createdAt"`
}
