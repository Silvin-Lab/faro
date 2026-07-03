# ADR-005 — Lealtad: de config única a lista de promociones

_Fecha: 2026-07-02 · Estado: **aceptado** (decisiones cerradas con el humano) · Módulo: M6 loyalty v2_

## Contexto

La v1 de lealtad (migración 0009, ya aplicada a `faro` y `faro_test`) modela **una
sola configuración por negocio**: dos "tiers" fijos (descuento X% a las X visitas +
gratis a las Y visitas), cada uno con su tabla de productos
(`loyalty_discount_products`, `loyalty_free_products`), un flag `enabled`, un contador
de ciclo `customers.visits`, y `sales.discount_cents` + `sales.loyalty_reward`
(`null|discount|free`). La aplicación es manual en el POS.

El negocio necesita **N promociones arbitrarias** (no dos tiers fijos). Ejemplo real:
(a) 50% para ciertos productos a las 3 visitas, sin reinicio; (b) 100% (gratis) para
otros productos a las 5 visitas, con reinicio del contador. Además:

- El programa **siempre está activo** y las **visitas se comparten entre sucursales**
  en todos los casos (deja de ser configurable → se elimina `enabled`).
- Al reiniciar el contador hay que **guardar un histórico/snapshot** para habilitar a
  futuro promociones de "clientes especiales que asistieron N veces" → se necesita un
  **acumulado de por vida** además del contador de ciclo, y una **tabla de historial**.
- El POS debe permitir **buscar cliente por teléfono y por nombre**, y mostrar el
  **detalle de elegibilidad** (visitas actuales + cuántas faltan por promoción +
  productos aplicables).

## Opciones consideradas

### A) Extender la config única con más tiers (columnas/JSON)
Añadir `tier3`, `tier4`… o un JSON de tiers dentro de `loyalty_configs`.
- Pros: cambio pequeño; una fila por negocio.
- Cons: no es un CRUD real; consultar "faltan N por promoción" sobre JSON es incómodo;
  el snapshot/historial y el vínculo promoción↔productos quedan mal normalizados. No
  escala a "clientes especiales" ni a métricas por promoción.

### B) Modelo de promociones como entidad de primera clase (elegida)
`loyalty_promotions` (1..N por negocio) + `loyalty_promotion_products` (M:N con
`products`) + `loyalty_redemptions` (historial de canjes/snapshots). Contador de ciclo
`customers.visits` + acumulado `customers.visits_lifetime`. La venta referencia la
promoción aplicada vía la fila de historial.
- Pros: CRUD natural; normalizado; soporta múltiples promociones, historial, y el
  futuro de tiers/"clientes especiales"; el cálculo de "faltan N" y "disponibles" es
  una consulta directa.
- Cons: más tablas y una migración de datos 0010 que transforma la config v1 en
  promociones antes de eliminar las tablas viejas.

### C) Motor de reglas genérico (condiciones/acciones configurables)
- Pros: máxima flexibilidad futura. Cons: **sobreingeniería** para el alcance actual;
  mucho más costoso de construir y validar. Descartada para el MVP.

## Decisión

**Opción B.** Se reemplaza `loyalty_configs` + tablas de productos v1 por un modelo de
**promociones**:

- `loyalty_promotions(id, tenant_id, name, discount_percent[1..100], visit_threshold>0,
  resets_counter bool, status, created_at, updated_at)`. `discount_percent = 100` ⇒
  producto gratis (deja de existir el tipo `free` separado).
- `loyalty_promotion_products(promotion_id, product_id, tenant_id)` — productos a los
  que aplica cada promoción.
- `loyalty_redemptions(...)` — **historial** de cada canje: snapshot de la promoción
  (nombre, %, umbral), `caused_reset`, contador de ciclo y de por vida alcanzados,
  `discount_cents`, `sale_id`, `customer_id`, timestamp.
- `customers`: se conserva `visits` como **contador de ciclo** y se añade
  `visits_lifetime` (acumulado que **nunca** se reinicia).
- `sales`: se conserva `discount_cents`; se **elimina** `loyalty_reward`. La promoción
  aplicada a una venta se registra en `loyalty_redemptions` (una fila por canje).

**Semántica adoptada** (decisiones cerradas con el humano):

- **Un solo contador de ciclo** compartido (`customers.visits` = visitas pagadas
  **completadas**, no incluye la venta en curso). `faltan = umbral − visits`.
- **Visita = venta pagada con cliente asociado**: cada venta hace `visits += 1` y
  `visits_lifetime += 1`.
- **Elegibilidad = A (se preserva v1): la venta en curso cuenta para su propia
  elegibilidad.** Una promoción es **aplicable en la venta actual** cuando
  `visits + 1 ≥ umbral` (la compra que **completa** el umbral ya cobra la recompensa).
  El **modal de detalle** muestra `faltan = umbral − visits` sobre el contador
  almacenado, de modo que el ejemplo del usuario se cumple literalmente (visits=2 →
  faltan 1 para promo de 3, faltan 3 para 5, faltan 5 para 7). `faltan ≤ 1 ⇔ aplicable`.
  Una promoción **sin reinicio** con `visits ≥ umbral` permanece aplicable en cada venta.
- El **reinicio** ocurre cuando el cajero **aplica manualmente** una promoción con
  `resets_counter = true` que efectivamente otorgó descuento: se escribe el snapshot al
  historial y `visits → 0`. El acumulado de por vida no se toca. Aplicación siempre
  **manual** en el POS.
- El **descuento se calcula en el servidor**: **una sola unidad** de un producto elegible,
  `discount_cents = round(precio_unitario × % / 100)`, acotado a `≤ subtotal` (100% = una
  unidad gratis). El cajero elige la unidad (`promotionProductId`); si se omite, el servidor
  toma el producto elegible de mayor precio en el carrito. Salda la deuda v1 de confiar el
  cálculo al cliente.

## Consecuencias

- Positivas: CRUD de promociones real; historial para métricas y "clientes especiales";
  cálculo de descuento confiable en servidor; contrato de venta más simple
  (`promotionId` en vez de `loyaltyReward`).
- Negativas / deuda:
  - Migración 0010 **destructiva** sobre datos v1 (transforma y luego elimina). En bajar
    (down) se pierde el historial de promociones y el detalle de `loyalty_reward` de
    ventas viejas. Aceptable: el sistema es reciente y los datos son de arranque.
  - `visits_lifetime` histórico es un **piso estimado** (se backfillea con `visits`
    actual; las visitas ya reiniciadas en v1 no se pueden reconstruir).

## Plan de migración de datos (0010)

Aplicada **encima** de 0009 (ya viva con datos). Orden en la transacción de `up`:

1. `CREATE TABLE loyalty_promotions`, `loyalty_promotion_products`,
   `loyalty_redemptions`.
2. `ALTER TABLE customers ADD COLUMN visits_lifetime integer NOT NULL DEFAULT 0;`
   luego `UPDATE customers SET visits_lifetime = visits;` (backfill piso).
3. **Transformar** cada `loyalty_configs` con datos en 0..2 promociones:
   - Si `discount_visits IS NOT NULL` y hay productos en `loyalty_discount_products`
     → promoción `name='Descuento'`, `discount_percent = discount_percent`,
     `visit_threshold = discount_visits`, `resets_counter = false`, con esos productos.
   - Si `free_visits IS NOT NULL` y hay productos en `loyalty_free_products`
     → promoción `name='Producto gratis'`, `discount_percent = 100`,
     `visit_threshold = free_visits`, `resets_counter = true`, con esos productos.
4. `ALTER TABLE sales DROP COLUMN loyalty_reward;` (se conserva `discount_cents`).
5. `DROP TABLE loyalty_discount_products, loyalty_free_products, loyalty_configs;`

`down`: recrear tablas v1 vacías, `ALTER TABLE sales ADD COLUMN loyalty_reward text`,
`ALTER TABLE customers DROP COLUMN visits_lifetime`, y `DROP` de las tablas v2. Se
documenta la pérdida de datos.
