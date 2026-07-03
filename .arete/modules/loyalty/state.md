# Estado — Módulo loyalty (M6)
_Actualizado: 2026-07-02_

## Etapa actual
📐 **Diseño v2 cerrado** (promociones) — tech-spec + ADR-005 listos, **sin preguntas
abiertas bloqueantes**; listo para construir. La v1 (config única) está en disco/DB (0009).

## v2 — de config única a promociones (resumen)
- Programa **siempre activo**, visitas **compartidas entre sucursales** (fijo; se elimina `enabled`).
- **CRUD de promociones**: `name`, `discount_percent` 1..100 (100 = gratis), `visit_threshold`,
  `resets_counter`, productos. Reemplaza los 2 tiers fijos.
- **Contador de ciclo** (`customers.visits`) + **acumulado de por vida** (`visits_lifetime`).
- **Historial** `loyalty_redemptions` (snapshot en cada canje; base para "clientes especiales").
- POS: búsqueda por **nombre y teléfono**; acciones **Quitar / Ver detalle**; detalle con
  faltantes por promoción (asc) y productos aplicables.
- **Una sola promoción por venta**. Descuento calculado en **servidor** (una unidad).
- Detalle completo: `tech-spec.md`. Decisión: `ADR-005`. Migración: **0010**.

## Decisiones cerradas (con el humano)
- Programa siempre activo + visitas compartidas (no configurable).
- Contador único de ciclo por cliente; `faltan = umbral − visits` (contador almacenado).
- **Elegibilidad = A:** la venta que **completa** el umbral cobra la recompensa
  (`visits + 1 ≥ umbral`); el modal muestra `faltan = umbral − visits`. Ejemplo: lleva 2 →
  faltan 1 para promo de 3 (y esa compra la desbloquea), 3 para 5, 5 para 7. Fórmula
  compartida back/front en `tech-spec §3.1`.
- **Descuento = una sola unidad** de un producto elegible: `round(precio_unitario × % /100)`,
  acotado al subtotal (100% = una unidad gratis). El cajero elige la unidad
  (`promotionProductId`) o el servidor toma la de mayor precio en el carrito.
- Reinicio = al aplicar manualmente en POS una promoción `resets_counter` que otorgó
  descuento; escribe snapshot y `visits→0`. Visitas de por vida nunca reinician.
- **Exactamente una promoción por venta** (nunca dos en la misma compra).
- `name` de promoción **obligatorio** y visible en el ticket.
- Baja de promo = **archivar** (`status='inactive'`).
- Promo aplicable pero sin sus productos en el carrito → **se cobra normal** sin descuento
  (no bloquea, no reinicia, no escribe historial).
- Promos solapadas → el **cajero elige** (una por venta). **Sin tope** de promos activas;
  un producto puede estar en **varias** promociones.

## Preguntas abiertas
Ninguna bloqueante. (Deuda futura no bloqueante: índice `pg_trgm` para búsqueda de clientes
si el volumen crece; reconstrucción exacta de `visits_lifetime` histórico no es posible.)

## Entregables de diseño v2
- `.arete/modules/loyalty/tech-spec.md` (v2) — modelo de datos, API, flujos POS.
- `.arete/foundations/adr/ADR-005-loyalty-promociones.md` — decisión + plan de migración.
- `.arete/db/er.md` — ER actualizado (tablas v2).

## Pipeline
| # | Gate | Estado |
|---|------|--------|
| 1-3 | PRD/diseño/tech-spec v2 | ✅ (decisiones cerradas) |
| 4 | Tareas | ⬜ |
| 5 | Build (migración 0010 + back + front) | ⬜ |
| 6-7 | QA / review | ⬜ |
| 8 | Deploy | ⬜ |
