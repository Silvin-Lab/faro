# ADR-008 — Almacén central: doble ledger y movimiento `transfer` en la sucursal

_Fecha: 2026-07-20 · Estado: **aceptado** · Módulo: M8 warehouse · Depende de: 0015_supplies (ledger por sucursal), ADR-006/007 (branches)_

## Contexto

M8 introduce un **almacén central único** por negocio (F1), intermedio entre "comprar" y
"la sucursal consume el insumo". El almacén lleva su **propio stock** por insumo (F3),
independiente del stock por sucursal que ya existe (`supply_branch_stock` + ledger
`supply_movements`, ver 0015).

El punto de fricción (R2 del PRD) es la **salida almacén→sucursal** (F8-F10): una salida
(dispatch) **resta** del stock del almacén y, a la vez, debe **sumar automáticamente** al
inventario de la sucursal destino (F9) y quedar como un **movimiento visible y con fecha
en el historial de esa sucursal** (F10), sin doble captura manual.

El sistema ya tiene un ledger firmado por sucursal (`supply_movements`, tipos
`purchase | adjustment | sale`) con la invariante
`supply_branch_stock.stock_base == SUM(supply_movements.quantity_base)` por
`(supply_id, branch_id)`, y un patrón de escritura transaccional (cache + ledger en la
misma tx, ver `insertMovement` y `deductSupplies`). El almacén no puede modelarse como una
"sucursal virtual": Silvin confirmó que es una **entidad de datos nueva**.

## Decisión

### D1 — Dos ledgers separados, uno por dominio de stock

- **Ledger del almacén (nuevo):** `warehouse_movements` es la fuente de verdad del stock
  central. Tipos `purchase (+) | dispatch (−) | waste (−)`. Su cache es `warehouse_stock`.
  Invariante: `warehouse_stock.stock_base == SUM(warehouse_movements.quantity_base)` por
  `supply_id`.
- **Ledger por sucursal (existente):** `supply_movements` sigue siendo la fuente de verdad
  del stock por sucursal. No se toca su invariante.

El stock del almacén y el stock de una sucursal son **cantidades distintas de dominios
distintos**. Una salida es **una** acción del usuario que cruza la frontera entre ambos
dominios: los mismos bienes **salen** del almacén (−) y **entran** a la sucursal (+). No es
doble contabilidad del mismo contador; es un traspaso entre dos contadores.

### D2 — La pata de sucursal de una salida es un `supply_movement` tipo `transfer` (nuevo)

Se agrega el tipo **`transfer`** al `CHECK` de `supply_movements.type`
(`purchase | adjustment | sale | transfer`). Una salida (dispatch) escribe, en **una sola
transacción**, cuatro efectos:

1. `warehouse_movements` (type=`dispatch`, cantidad **negativa**, `branch_id` = destino).
2. `warehouse_stock` −= cantidad.
3. `supply_movements` (type=`transfer`, cantidad **positiva**, `branch_id` = destino,
   `warehouse_movement_id` = id del dispatch origen).
4. `supply_branch_stock` += cantidad (upsert, mismo mecanismo que `insertMovement`).

`transfer` es **positivo** (entra a la sucursal). Se distingue de `purchase` (compra
directa a sucursal, legado) y de `sale` (descuento por venta). El historial de inventario
de la sucursal (endpoint existente `GET /supplies/{id}/movements`) muestra el `transfer`
con su fecha → **F10 se cumple sin superficie nueva** en el módulo de sucursal.

**Corolario — `waste` en sucursal (merma F11-b).** Por el mismo principio de "cada dominio
descuenta lo que realmente perdió", la **merma de producto ya despachado a una sucursal** se
escribe como `supply_movements` tipo **`waste`** (−, sobre `supply_branch_stock`), **no**
sobre el almacén: esos bienes ya salieron del almacén vía `dispatch`, descontarlo otra vez
sería doble contabilidad invertida (déficit fantasma en el almacén + sucursal inflada). La
merma **en** el almacén (F11-a, sin sucursal) sí vive en `warehouse_movements.waste`. Es un
concepto de negocio único que bifurca por ubicación física de los bienes. Se añade también
`waste` al `CHECK` de `supply_movements.type`. Detalle en el tech-spec §3.3/§4.3.

### D3 — Trazabilidad por FK, no por reconciliación

Se agrega `supply_movements.warehouse_movement_id` (uuid NULL, FK a `warehouse_movements`
ON DELETE SET NULL). Liga la pata de sucursal (`transfer`) con su salida de almacén origen.
Beneficios: auditoría de extremo a extremo, y evita movimientos `transfer` huérfanos o
duplicados. Ambas patas nacen en la misma transacción → nunca hay una sin la otra.

### D4 — La API manual de sucursal no acepta `transfer`

`transfer` solo lo escribe el store de almacén dentro de la transacción de dispatch.
`POST /supplies/{id}/movements` (capa `supplies.CreateMovement`) sigue rechazando cualquier
type ≠ `purchase | adjustment` (igual que hoy rechaza `sale`). Nadie puede fabricar un
`transfer` sin su contraparte de almacén.

## Alternativas consideradas

- **A) `transfer` nuevo (elegida).** Auditable, semánticamente claro, cero doble
  contabilidad (dominios separados), reusa el patrón transaccional existente. Contra: el
  frontend de la historia de sucursal debe etiquetar un tipo más.
- **B) Reusar `purchase` para la pata de sucursal.** Cero cambios de esquema. Contra:
  miente sobre el origen (no fue una compra a proveedor), rompe la trazabilidad y ensucia
  reportes de compra por sucursal. Descartada.
- **C) Almacén como "sucursal virtual" en `supply_branch_stock`.** Reusaría todo el ledger
  existente. Contra: Silvin lo descartó explícitamente; mezcla min/máx de almacén con stock
  de sucursal, obliga a filtrar la sucursal-fantasma en todos los reportes y en
  `deductSupplies`. Descartada.
- **D) Un único ledger unificado con dimensión almacén/sucursal.** Elegante en papel.
  Contra: migración disruptiva de una tabla en producción, reescribe `deductSupplies` y
  todo `supplies`. Sobreingeniería para el MVP. Descartada.

## Consecuencias

- **Positivas:** invariantes limpias e independientes por dominio; F9/F10 automáticas y
  auditables; patrón transaccional consistente con lo ya desplegado; superficie mínima en
  el módulo de sucursal (un tipo nuevo + una columna FK).
- **Negativas / deuda:**
  - Dos ledgers que hay que mantener coherentes en las transacciones que los cruzan (solo
    el dispatch los cruza; encapsulado en un método de store).
  - El frontend de `supplies/[id]` (historial, si se conserva) debe etiquetar `transfer`.
  - No hay reconciliación automática entre ambos ledgers; la FK da trazabilidad pero un
    reporte "cuánto salió del almacén vs. cuánto entró a sucursales" es trabajo futuro.
- **Migración:** `0019_warehouse` — crea `suppliers`, `warehouse_stock`,
  `warehouse_movements`; altera el `CHECK` de `supply_movements.type` para sumar `transfer`
  **y `waste`**; agrega `supply_movements.warehouse_movement_id`. No destructiva; `down`
  revierte columnas, tipos y tablas.
- **R3 (mermas) y R4 (stock negativo):** resueltas en el tech-spec del módulo
  (`.arete/modules/warehouse/tech-spec.md`): la merma es un concepto único que **bifurca por
  ubicación** (almacén → `warehouse_movements.waste`; sucursal → `supply_movements.waste`) y
  **coexiste** con `adjustment`; el stock **permite negativo** (no bloqueante) en ambos
  ledgers, consistente con `supply_branch_stock`.
</content>
</invoke>
