# ADR-010 — Descuentos de convenio: columnas separadas en `sales`, orden de cálculo y usuario de la venta

_Fecha: 2026-10-07 · Estado: **aceptado** · Módulo: M12 convenio-discounts · Depende de: 0004_sales, 0009/0010 (lealtad v2 + `loyalty_redemptions`), 0011_branches (`sales.branch_id`), ADR-005 (patrón CRUD lealtad), ADR-007 (sucursal activa de la sesión), ADR-009 (insights sobre `loyalty_redemptions`)_

> Nota de numeración: el brief sugería ADR-009 como próximo libre, pero ADR-009 (insights) ya existe en `foundations/adr/`. Este ADR toma el siguiente número real: **010**.

## Contexto

M12 agrega **descuentos de convenio** al POS: un % sobre toda la compra que se **suma** al
descuento de lealtad existente. Decisiones de producto ya cerradas por el dueño (no se
re-litigan): lealtad + convenio se suman; botones solo con %; aplica a toda la compra; lo
otorga cualquier rol; catálogo dinámico con seed 10% y 15%; la venta queda ligada al usuario
de la sesión.

Tres decisiones técnicas son relevantes y caras de revertir una vez que haya ventas en prod
con el esquema nuevo, y se resuelven aquí.

1. **¿Dónde se guarda el monto del convenio en `sales`?** Hoy `sales.discount_cents` guarda
   **solo el descuento de lealtad** (lo escribe la transacción de venta, lo refleja el modelo
   `Sale`, y `loyalty_redemptions.discount_cents` guarda el mismo valor como fuente de verdad
   del canje). `sales.total_cents` ya es **neto** (`subtotal − lealtad`). Opciones: (A) reusar
   `discount_cents` como "descuento total" (lealtad + convenio); (B) dejar `discount_cents`
   solo para lealtad y agregar `agreement_discount_cents`.

2. **Orden de aplicación y redondeo.** Lealtad (descuento de UNA unidad, ya existente) y
   convenio (% sobre la compra) deben componerse de forma determinista y cuadrar al centavo.

3. **Atribución de la venta a un usuario.** `sales` nunca guardó quién cobró; se pide ahora
   para toda venta nueva, con las históricas en NULL.

## Decisión

### D1 — `discount_cents` queda **solo para lealtad**; se agrega `agreement_discount_cents` (opción B)

`sales` gana columnas nuevas, aditivas y nullables donde aplica:

- `agreement_discount_id   uuid NULL REFERENCES agreement_discounts(id) ON DELETE SET NULL` — qué descuento del catálogo se usó (trazable; sobrevive al archivado y se vuelve NULL si el catálogo se borra duro, cosa que no hacemos).
- `agreement_discount_percent integer NULL CHECK (… BETWEEN 1 AND 100)` — **snapshot** del % al momento de la venta (inmune a ediciones posteriores del catálogo).
- `agreement_discount_cents integer NOT NULL DEFAULT 0` — monto del convenio, análogo a `discount_cents`.

`discount_cents` **no cambia de significado**: sigue siendo el descuento de lealtad.
`total_cents` sigue siendo neto, ahora de **ambos** descuentos (`subtotal − lealtad − convenio`).

Fundamento (qué rompe menos):
- **Reportes:** suman `total_cents` (neto) — correctos sin tocar nada. El bloque nuevo de
  convenio necesita un monto de convenio **aislado**; si `discount_cents` fuera el total,
  habría que restar lealtad por fuera (vía `loyalty_redemptions`) para reconstruir el convenio
  — frágil y duplicado.
- **Insights (ADR-009):** no lee `sales.discount_cents` (usa `loyalty_redemptions`), pero
  cualquier consumidor que asuma "`discount_cents` = lealtad" seguiría siendo válido.
- **Ticket / detalle de venta:** necesita mostrar lealtad y convenio **por separado**
  (requisito del brief). Columnas separadas lo dan directo; un único total mezclado obligaría
  a separar en el cliente.
- **Consistencia:** `loyalty_redemptions.discount_cents` ya es la fuente de verdad de la
  lealtad y seguirá cuadrando con `sales.discount_cents`. No se introduce divergencia.

### D2 — Orden fijo: lealtad primero, convenio sobre el subtotal restante; redondeo half-up en centavos

En la transacción de venta (servidor, nunca el cliente):

1. `subtotal` = suma de líneas a precio propio (sin cambios).
2. `loyaltyCents` = descuento de lealtad existente (una unidad), ya calculado y acotado a `subtotal`.
3. `remaining = subtotal − loyaltyCents`.
4. `agreementCents = (remaining * percent + 50) / 100` (división entera ⇒ **half-up** en
   centavos), acotado a `[0, remaining]`. Misma convención de redondeo que ya usa lealtad
   (`store.go`: `(unitCents*pct + 50) / 100`) — una sola regla en todo el POS.
5. `total = subtotal − loyaltyCents − agreementCents` (≥ 0 por construcción).

El convenio se calcula **después** de la lealtad y sobre el neto, no sobre el bruto: así nunca
se descuenta dos veces la misma base y el total cuadra exacto al centavo. La interacción con el
pago **no cambia**: `isExactPaymentMethod` (card/transfer/didi) fija `amountPaid = total` sin
cambio; efectivo valida `amountPaidCents ≥ total` y calcula el cambio. Todo opera sobre el
`total` ya neto de ambos descuentos.

### D3 — Convenio requiere cliente y un descuento **activo** del tenant; `agreement_discounts` con % único por tenant entre los activos

- El convenio **requiere `customerId`** (igual que lealtad); sin cliente, el servidor ignora
  `agreementDiscountId` (y la UI deshabilita los botones). No es error duro por omisión, pero
  si llega `agreementDiscountId` sin cliente el servidor lo trata como no aplicable.
- El descuento debe existir, estar `active` y pertenecer al tenant; si no ⇒ error
  `agreement_discount_not_eligible` (422). No se confía en el % del cliente: se lee del catálogo
  y se guarda como snapshot.
- **Unicidad del %:** índice **único parcial** `(tenant_id, percent) WHERE status='active'`. El
  archivado (soft-delete `status='inactive'`) no colisiona con un nuevo activo del mismo %,
  preservando "un % único por tenant" solo entre los vigentes.

### D4 — `sales.user_id` nullable; se puebla desde la sesión en toda venta nueva

`sales` gana `user_id uuid NULL REFERENCES users(id)`. La transacción lo recibe del
`auth.UserFromContext` del handler (nunca del cliente). Históricas = NULL ("sin registro"). Se
expone en el detalle y el historial como `soldByUserId` + `soldByName` (NULL ⇒ el cliente
muestra "Sin registro").

## Consecuencias

**Positivas**
- Lealtad y convenio quedan medibles por separado sin reconstrucciones; reportes de total
  siguen correctos sin cambios de semántica.
- Snapshot de % e id hacen la venta auditable e inmune a ediciones del catálogo.
- Una sola regla de redondeo en todo el POS; el ticket cuadra al centavo.
- Atribución por usuario disponible para toda venta nueva, retrocompatible (nullable).

**Negativas / riesgos**
- Toca la transacción de venta (lo más usado en prod) ⇒ regresión obligatoria (ver tech-spec §5).
- `sales` crece en 4 columnas; la migración 0031 se aplica **a mano** sobre una base en 0020
  (0021–0030 reservadas por M10/M11), por lo que el estado de migraciones queda no contiguo
  hasta que M10/M11 se integren — aceptado porque este módulo sale antes (ver tech-spec §6).

## Alternativas consideradas

- **(A) `discount_cents` = descuento total (lealtad + convenio):** descartada — rompe el
  significado histórico de la columna, obliga a separar convenio por fuera para el reporte y el
  ticket, y arriesga divergencia con `loyalty_redemptions`. Más frágil, no más simple.
- **Convenio sobre el bruto (subtotal, en paralelo a lealtad):** descartada — puede dejar
  `total < 0` en compras muy pequeñas con lealtad alta y "descuenta dos veces" la base; el
  orden secuencial es el que el dueño describió ("luego el % sobre el subtotal restante").
- **Unicidad dura `(tenant_id, percent)` sin filtro de status:** descartada — impediría volver a
  crear un 10% tras archivarlo; el índice parcial resuelve el soft-delete.
- **Tabla de atribución aparte (no columna en `sales`):** descartada — sobreingeniería; es un
  atributo 1:1 de la venta.
