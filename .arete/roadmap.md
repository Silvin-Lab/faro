# Roadmap — Faro
_Actualizado: 2026-06-29_

## Objetivo del proyecto
Sistematizar los procesos clave de una cafetería (productos, ventas, reportes, lealtad) con un POS integrado. MVP instalable en un negocio en 4 días.

## Módulos
| #  | Módulo (slug)         | Descripción                                                                 | Estado     | Orden | Depende de |
|----|-----------------------|-----------------------------------------------------------------------------|------------|-------|------------|
| M1 | login                 | Autenticación de usuarios (sin roles en v1)                                  | 👁 review ✅ (pre-deploy) | 1 | — |
| M2 | category-management   | CRUD de categorías de producto                                              | 👁 review ✅ (pre-deploy) | 2 | M1 |
| M3 | product-management    | CRUD de productos (precio, categoría, estado)                               | 👁 review ✅ (pre-deploy) | 3 | M2 |
| M4 | pos                   | Comanda/venta, agregar productos, cobro con cálculo de cambio, ticket       | 👁 review ✅ (pre-deploy) | 4 | M3 |
| M5 | sales-reports         | Reportes diarios y por periodo, agrupados por categoría y horario           | 👁 review ✅ (pre-deploy) | 5 | M4 |
| M6 | loyalty               | Alta por teléfono, puntos, tarjeta QR wallet iOS/Android, WhatsApp, redención| 🔨 entrada iniciada (clientes ✅) | 6 | M3, M4 |
| M7 | business-settings     | Negocio único: super admin administra sucursales (M:N), usuarios y favicon; sucursal activa de sesión etiqueta ventas | 🎨 diseño listo (docs; ADR-006/007) | 7 | M1, M4, M5 |
| M8 | warehouse             | Almacén central (único) con mín/máx y alerta de reposición; Compras (+ catálogo de proveedores), Salidas a sucursal, Mermas; reorg de navegación desde /supplies/[id]/edit | 🔍 discovery (brief) | 8 | supplies, business-settings |

_Estados: 💡 idea · 📋 backlog · 🔍 discovery · 🎨 design · 🟢 ready · 🔨 in-dev · 🧪 qa · 👁 review · 🚀 deployed · ✅ done_

## Corte del MVP (✅ confirmado)
- **MVP 4 días = M1 + M2 + M3 + M4** (login, categorías, productos, POS).
- **POS v1:** online + impresora térmica ESC/POS. Migración a **offline** en fase posterior.
- **Día 5+ (post-MVP):** M5 reportes y M6 lealtad.

## Riesgos / dependencias
- **M4 (POS):** v1 **online + impresora térmica** (decidido). Offline queda como fase posterior (impacto arquitectónico a planear por el tech-lead cuando toque migrar).
- **M6 (lealtad):** tarjetas wallet requieren **Apple Developer (PassKit)** y **Google Wallet API**; el envío por **WhatsApp** requiere WhatsApp Business API / proveedor. Cuentas externas con onboarding y costo → **iniciar el trámite cuanto antes** para no bloquear el día 5+.

## Notas
- Documento **vivo**: se agregan/reordenan módulos sin tocar los terminados ni el charter.
- v1 sin roles; "roles y permisos" entrará como módulo futuro.
- "Inventario" se menciona en el objetivo: pendiente decidir si entra como módulo propio tras el MVP.
- **Roadmap desactualizado:** hay módulos ya construidos y en prod que esta tabla aún no refleja (expenses, supplies con categorías/medidas de uso, roles, branches). La numeración "M8" de `warehouse` es el siguiente número libre en esta tabla; puede no coincidir con la numeración conceptual de los módulos no reflejados (p.ej. "roles"). Pendiente reconciliar el historial en una pasada futura.
