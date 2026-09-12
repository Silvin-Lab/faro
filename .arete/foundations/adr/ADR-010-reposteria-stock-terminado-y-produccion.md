# ADR-010 — Repostería: stock de producto terminado, receta de doble semántica y transacción de producción

_Fecha: 2026-08-13 · Estado: **aceptado** · Módulo: M10 bakery · Depende de: 0015_supplies (recetas + ledger de sucursal), ADR-008 (almacén doble ledger), ADR-007 (negocio único / sucursal activa)_

## Contexto

M10 sistematiza el flujo **pedido de sucursal → producción en la repostería central →
acreditación de stock del postre a la sucursal**. Introduce tres tensiones de diseño que
cruzan módulos maduros (`sales`, `warehouse`, `supplies`, `products`, `auth`) y que
conviene fijar como decisión trazable antes de construir:

1. **El producto terminado (postre) necesita stock propio por sucursal**, cosa que hoy no
   existe: los productos del catálogo no tienen inventario. El resto de productos
   (`branch_prepared`) siguen sin stock propio; solo los postres (`bakery`) lo tienen.
2. **La receta (`product_supplies`) adquiere doble semántica** según
   `products.fulfillment_type`: para `branch_prepared` la receta se consume **al vender**
   (comportamiento actual, `deductSupplies`); para `bakery` se consume **al producir** en la
   central, y la venta **NO** debe re-descontar insumos (evitar doble contabilidad, R1 del
   PRD).
3. **La producción es una transacción de doble efecto que cruza dos dominios de stock**
   (los mismos dominios de ADR-008): descuenta insumos del **almacén central**
   (`warehouse_stock`/`warehouse_movements`) y acredita el postre al **stock de la
   sucursal** (`product_branch_stock`/`product_stock_movements`, nuevos).

El punto de fricción es el mismo que resolvió ADR-008 para el almacén: mantener cada stock
como cache de un **ledger firmado** que es la única fuente de verdad, con escritura
**siempre en la misma transacción** (sin ventana de inconsistencia), reusando patrones ya
en producción (`CreateDispatch`, `deductSupplies`).

## Decisión

### D1 — Stock de producto terminado como tercer dominio de stock (cache + ledger)

Se crea un **dominio de stock nuevo, análogo a los dos de ADR-008**:

- **Cache:** `product_branch_stock` (`product_id`, `branch_id`, `stock_qty`), un renglón por
  (producto, sucursal), `stock_qty` **sin CHECK de no-negatividad** (mismo criterio que
  `supply_branch_stock`/`warehouse_stock`).
- **Ledger (fuente de verdad):** `product_stock_movements`, firmado, tipos
  `production_in (+) | sale (−) | adjustment (±) | waste (−)`. Invariante:
  `product_branch_stock.stock_qty == SUM(product_stock_movements.quantity)` por
  (`product_id`, `branch_id`).

Se modela como **stock por sucursal** (no un stock global del postre) porque la
acreditación es a una sucursal concreta (F11) y la venta descuenta de la sucursal donde
ocurre. Solo participan productos `fulfillment_type='bakery'`; los `branch_prepared` nunca
tienen filas aquí (su "stock" sigue siendo implícito vía insumos).

### D2 — `product_supplies` con doble semántica gobernada por `fulfillment_type` (sin duplicar tabla)

No se crea una tabla de receta separada para postres. Se **reinterpreta** `product_supplies`
según `products.fulfillment_type`:

- `branch_prepared` → consumo **al vender** (comportamiento actual intacto).
- `bakery` → consumo **al producir** una unidad en la central; descuenta el **almacén
  central**, no la sucursal.

La disyunción se implementa como un `JOIN ... AND p.fulfillment_type = '<tipo>'` en el punto
de consumo de cada flujo, de modo que **un mismo insumo nunca se descuenta dos veces** y un
carrito mixto (café `branch_prepared` + postre `bakery`) reparte correctamente. Se prefiere
esto a duplicar la tabla de recetas porque el editor de receta y la UI son idénticos; la
diferencia es *cuándo* se aplica, no *qué* contiene.

### D3 — Transacción de producción: doble efecto que cruza almacén→postre, réplica de `CreateDispatch`

`POST /bakery/orders/{id}/produce` ejecuta en **una sola transacción** (patrón probado en
`internal/warehouse/store.go insertDispatch`), con `SELECT ... FOR UPDATE` sobre
`bakery_orders` para **serializar producciones concurrentes** contra el mismo pedido (R3):

1. Bloquea y valida el pedido (`status IN ('pending','in_production')`, producto sigue
   `bakery`).
2. Inserta `bakery_productions`.
3. Por cada insumo de la receta (ordenado por `supply_id`, anti-deadlock): movimiento
   `warehouse_movements(type='production', −consumo, branch_id NULL)` + upsert
   `warehouse_stock`.
4. Movimiento `product_stock_movements(type='production_in', +producido, branch destino)` +
   upsert `product_branch_stock`.
5. Actualiza `quantity_shipped` y el estado (`shipped` si alcanza lo pedido, si no
   `in_production`).

El `type='production'` en `warehouse_movements` lleva `branch_id NULL` (el insumo se consume
centralmente, no se despacha a una sucursal) y `bakery_production_id` para trazabilidad
(mismo patrón que `supply_movements.warehouse_movement_id`).

**Nota de implementación:** `internal/bakery/store.go` escribe SQL directo contra las tablas
de `warehouse` y contra `product_branch_stock`/`product_stock_movements`, en vez de exportar
funciones de otros módulos. Mismo criterio ya vigente en `sales.deductSupplies` (que escribe
contra las tablas de `supplies`): evita refactors de riesgo en módulos maduros.

### D4 — La venta bifurca por tipo: postre descuenta stock terminado, no insumos

En `internal/sales/store.go`:

- `deductSupplies` gana `AND p.fulfillment_type='branch_prepared'`: un postre **no** dispara
  descuento de insumos en la sucursal (ya se consumieron al producir).
- Nueva `deductFinishedGoods` (misma estructura, llamada justo después): descuenta
  `product_branch_stock` y escribe `product_stock_movements(type='sale')` para líneas
  `fulfillment_type='bakery'`.

Ambas conviven en la misma transacción de venta; cada una filtra por tipo en su propio JOIN.

### D5 — Recepción (`received`) es informativa: no mueve stock

El stock se acredita **al producir** (D3, F11). Marcar `received` (F14) solo cambia el
estado del pedido para trazabilidad; no hay paso "en tránsito" (confirmado con Silvin, sin
transportista ni demora).

## Alternativas consideradas

- **Stock terminado global (no por sucursal):** rechazado; la acreditación y la venta son
  por sucursal, un stock global no reflejaría dónde está el postre.
- **Tabla de receta separada para postres:** rechazado; duplica UI y lógica de captura sin
  beneficio; la doble semántica se resuelve con el `fulfillment_type` en el JOIN.
- **Descontar insumos al vender el postre (como cualquier producto):** rechazado; produce
  doble contabilidad (los insumos ya se consumieron al producir). Es exactamente el bug que
  R1 del PRD pide evitar.
- **Estado "en tránsito" con acreditación diferida:** fuera de alcance (brief); la entrega
  es directa/manual.

## Consecuencias

- **Positivas:** trazabilidad completa (pedido→producción→venta, todo con ledger firmado);
  invariantes homogéneas con ADR-008; sin doble contabilidad; concurrencia acotada por
  `FOR UPDATE`.
- **Costo/deuda:** se toca `warehouse` (una ruta de lectura se abre a `repostero`, ver
  tech-spec §7) y `sales` (dos funciones); ambos requieren **tests de regresión**. La doble
  semántica de `product_supplies` es un punto que hay que documentar fuerte en la migración y
  en el código (fácil de malinterpretar por quien no conozca este ADR).
- **Migración de datos:** ninguna. `fulfillment_type` nace con default `branch_prepared`
  (preserva el comportamiento actual sin backfill).

## Desviación respecto del diseño técnico original (plan Feature 2)

El plan asumía que `warehouse_movements.type` tenía solo `purchase|dispatch|waste`. La
migración `0020_warehouse_adjustment` (aplicada después de escribirse el plan) ya añadió
`adjustment`. Por tanto, la migración de producción **extiende** el CHECK a
`('purchase','dispatch','waste','adjustment','production')` (no reemplaza a 3 tipos). Ver
tech-spec §2.6.
</content>
</invoke>
