// Package settings agrupa los ajustes de marca de un negocio (tenant-scoped). En
// M7 gestiona el favicon (URL en tenants.favicon_url, subida vía /uploads). Ver
// ADR-006 §D4 (deuda: migrar a tenant_settings si crecen los ajustes).
package settings

// TenantRef identifica al negocio en la respuesta de ajustes.
type TenantRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Settings es la vista de ajustes del negocio.
type Settings struct {
	Tenant     TenantRef `json:"tenant"`
	FaviconURL *string   `json:"faviconUrl"`
}
