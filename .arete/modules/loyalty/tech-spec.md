# Tech Spec — loyalty v2 (promociones por visitas)

_Fecha: 2026-07-02 · Módulo: M6 · Reemplaza la v1 (config única, migración 0009)_
_Decisión de arquitectura: ADR-005. **Estado: decisiones cerradas — sin preguntas abiertas bloqueantes.**_

## 1. Objetivo y cambios respecto a v1

Evolucionar la lealtad de **una config única de dos tiers** a un **CRUD de promociones**
arbitrarias, con **historial de canjes** y **acumulado de visitas de por vida**, más
mejoras de POS (búsqueda por nombre, detalle de elegibilidad).

| Aspecto | v1 (actual) | v2 (este spec) |
|--------|-------------|----------------|
| Config | 1 fila `loyalty_configs` con 2 tiers + `enabled` | N `loyalty_promotions` (CRUD). Sin `enabled` (siempre activo) |
| Productos | `loyalty_discount_products`, `loyalty_free_products` | `loyalty_promotion_products` (M:N por promoción) |
| Recompensa | `discount` / `free` | `discount_percent` 1..100 (100 = gratis), **una unidad** de un producto elegible |
| Contador | `customers.visits` (ciclo) | `customers.visits` (ciclo) + `visits_lifetime` (de por vida) |
| Historial | — | `loyalty_redemptions` (snapshot por canje) |
| Venta | `sales.discount_cents` + `sales.loyalty_reward` | `sales.discount_cents` + fila en `loyalty_redemptions` (sin `loyalty_reward`) |
| Cálculo descuento | cliente calcula, servidor acota | **servidor calcula** desde la promoción |
| Aplicación | manual, 1 recompensa/venta | manual, **exactamente 1 promoción/venta** (confirmado) |

## 2. Modelo de datos (migración 0010)

> 0009 ya está aplicada a `faro` (viva, con datos) y `faro_test`. 0010 se aplica encima:
> crea lo nuevo, **transforma** los datos v1 y luego **elimina** las tablas v1. Ver
> ADR-005 §"Plan de migración". El SQL de abajo es **diseño** — lo implementa
> backend-engineer como `migrations/0010_loyalty_promotions.{up,down}.sql`.

### Tablas nuevas

```
loyalty_promotions
  id               uuid PK        default gen_random_uuid()
  tenant_id        uuid NOT NULL  REFERENCES tenants(id)
  name             text NOT NULL                 -- etiqueta visible (ej. "50% café")
  discount_percent integer NOT NULL CHECK (discount_percent BETWEEN 1 AND 100)  -- 100 = gratis
  visit_threshold  integer NOT NULL CHECK (visit_threshold > 0)  -- umbral de visitas
  resets_counter   boolean NOT NULL DEFAULT false -- al aplicarla, visits → 0 + snapshot
  status           text NOT NULL DEFAULT 'active' -- active | inactive (archivada)
  created_at       timestamptz NOT NULL DEFAULT now()
  updated_at       timestamptz NOT NULL DEFAULT now()
  INDEX (tenant_id, status)

loyalty_promotion_products
  promotion_id  uuid NOT NULL REFERENCES loyalty_promotions(id) ON DELETE CASCADE
  product_id    uuid NOT NULL REFERENCES products(id) ON DELETE CASCADE
  tenant_id     uuid NOT NULL REFERENCES tenants(id)   -- denormalizado para aislamiento
  PRIMARY KEY (promotion_id, product_id)
  INDEX (product_id)

loyalty_redemptions            -- historial / snapshot de cada canje
  id                 uuid PK        default gen_random_uuid()
  tenant_id          uuid NOT NULL  REFERENCES tenants(id)
  customer_id        uuid NOT NULL  REFERENCES customers(id)
  sale_id            uuid NOT NULL  REFERENCES sales(id)
  promotion_id       uuid           REFERENCES loyalty_promotions(id) ON DELETE SET NULL
  -- snapshot de la promoción al momento del canje (sobrevive a ediciones/borrado):
  promotion_name     text    NOT NULL
  discount_percent   integer NOT NULL
  visit_threshold    integer NOT NULL
  caused_reset       boolean NOT NULL          -- ¿esta aplicación reinició el contador?
  -- estado del cliente en el instante del canje:
  visits_cycle_at    integer NOT NULL          -- contador de ciclo alcanzado (post-incremento)
  visits_lifetime_at integer NOT NULL          -- acumulado de por vida en ese instante
  discount_cents     integer NOT NULL
  created_at         timestamptz NOT NULL DEFAULT now()
  INDEX (tenant_id, customer_id, created_at)
```

### Cambios a tablas existentes

```
customers ADD COLUMN visits_lifetime integer NOT NULL DEFAULT 0   -- acumulado, nunca reinicia
          -- 'visits' se mantiene y pasa a documentarse como "contador de ciclo"
sales     DROP COLUMN loyalty_reward                              -- reemplazado por loyalty_redemptions
          -- 'discount_cents' se mantiene
```

### Qué pasa con las tablas v1
- `loyalty_configs`, `loyalty_discount_products`, `loyalty_free_products` → **eliminadas**
  en 0010, tras transformar sus datos en promociones (ADR-005 §migración, paso 3).

### Notas de modelado
- **Snapshot vs. referencia:** `loyalty_redemptions` guarda copia del nombre/%/umbral
  para que el historial no cambie si luego se edita/archiva la promoción (mismo criterio
  que `sale_items` con `name`/`unit_price_cents`). `promotion_id` se conserva como enlace
  débil (`ON DELETE SET NULL`).
- **`visits_lifetime` histórico:** se backfillea con `visits` actual (piso). No es
  reconstruible con exactitud porque v1 ya reiniciaba en los "free".
- **Búsqueda por nombre (POS):** requiere `ILIKE` sobre `customers.first_name/last_name`.
  Para volumen bajo basta un scan por `tenant_id`; si crece, evaluar `pg_trgm` +
  índice GIN (deuda futura, no bloqueante).

## 3. Reglas de negocio (semántica) — CERRADA

- **Contador único de ciclo** `customers.visits` = **visitas pagadas completadas**
  (NO incluye la venta en curso), compartido entre sucursales. Acumulado
  `visits_lifetime` nunca reinicia.
- **Visita = venta pagada con cliente asociado.** Cada venta: `visits += 1` y
  `visits_lifetime += 1`.

### 3.1 Fórmula de elegibilidad (idéntica en backend y frontend)

La venta en curso **sí** cuenta para su propia elegibilidad (se preserva la semántica v1:
la compra que **completa** el umbral ya cobra la recompensa). Sobre el contador
**almacenado** `visits` (antes de esta venta):

```
visitsRemaining   = visit_threshold - visits        // "faltan" (lo que muestra el modal); se muestra clamp a >= 0
redeemedThisCycle = ∃ redención de esta promo en el ciclo actual (ver abajo)
applicableNow     = (visits + 1) >= visit_threshold  AND NOT redeemedThisCycle
```

**Una promoción es redimible UNA sola vez por ciclo.** Alcanzar el umbral la vuelve
aplicable, pero al **redimirla** se "consume" y deja de estar disponible hasta que empiece
un nuevo ciclo. Si el cajero **no** la aplica en la visita del umbral, la promo **sigue
disponible** en visitas posteriores hasta que se redima (rollover; no desaparece por no
usarse). Esto corrige el defecto en el que una promo sin reinicio quedaba `applicableNow`
para siempre tras superar el umbral.

**Definición de "ciclo actual" y "redimida en el ciclo":**
- `last_reset_at` = `MAX(created_at)` de las filas de `loyalty_redemptions` del cliente con
  `caused_reset = true`. Si no hay ninguna → el ciclo abarca desde el inicio (piso `'epoch'`).
- Una promo **P** está **redimida en el ciclo actual** (`redeemedThisCycle = true`) si existe
  una fila en `loyalty_redemptions` con `customer_id = ?`, `promotion_id = P` y
  `created_at > last_reset_at`. La fila que **causó** el reinicio (`caused_reset = true`)
  tiene `created_at = last_reset_at`, por lo que pertenece al ciclo que **cerró**, no al nuevo
  (se excluye con desigualdad estricta).

Consecuencias de la semántica de ciclo:
- Una promo con `resets_counter = true` que se redime pone `visits = 0` → nuevo ciclo: todas
  las promos vuelven a estar disponibles (sus redenciones previas quedan en el ciclo cerrado).
- Para promos sin reinicio, el ciclo solo cambia cuando **otra** promo con `resets_counter`
  se redime. Mientras tanto, una promo redimida no reaparece como aplicable.

Consulta backend usada (elegibilidad de status y validación de venta):
```sql
-- conjunto de promociones redimidas en el ciclo actual del cliente
SELECT DISTINCT promotion_id
  FROM loyalty_redemptions
 WHERE tenant_id = $1 AND customer_id = $2 AND promotion_id IS NOT NULL
   AND created_at > COALESCE(
         (SELECT MAX(created_at) FROM loyalty_redemptions
           WHERE tenant_id = $1 AND customer_id = $2 AND caused_reset = true),
         'epoch'::timestamptz);
```

Ejemplo (contador almacenado `visits = 2`), tres promos de umbral 3, 5 y 7, **ninguna
redimida en el ciclo**:

| Promo | umbral | visitsRemaining | redeemedThisCycle | applicableNow | Texto en el modal |
|-------|--------|-----------------|-------------------|---------------|-------------------|
| A | 3 | **1** | false | **true** | "A: se desbloquea con esta compra" |
| B | 5 | 3 | false | false | "Faltan 3 visitas para B" |
| C | 7 | 5 | false | false | "Faltan 5 visitas para C" |

Si A ya se redimió en este ciclo: `redeemedThisCycle = true` y `applicableNow = false`
(no reaparece hasta un nuevo ciclo), aunque `visitsRemaining` siga siendo `≤ 1`.

Reconciliación del off-by-one: el modal muestra `faltan = umbral − visits` (por eso
"faltan 1" para la promo de 3 con `visits=2`, cumpliendo el ejemplo del usuario al pie de
la letra), y **esa misma compra** es la que completa el umbral, por lo que A es aplicable
ahora (si no fue ya redimida en el ciclo). Presentación por tramo (orden ascendente por
`visitsRemaining`):
- `visitsRemaining ≥ 2` → "Faltan {r} visitas para {name}" (informativo, no seleccionable).
- `visitsRemaining == 1` → "{name}: se desbloquea con esta compra" (seleccionable/aplicable
  si `!redeemedThisCycle`).
- `visitsRemaining ≤ 0` → "{name}: disponible" (seleccionable si `!redeemedThisCycle`; ya
  desbloqueada en una compra anterior; ocurre con promos **sin reinicio** cuyo `visits`
  superó el umbral y que aún no se redimieron en el ciclo).

### 3.2 Aplicación, descuento y reinicio

- **Exactamente una promoción por venta.** Si varias son aplicables, el cajero elige una;
  el servidor rechaza la venta si llegan ≥ 2 promociones (`multiple_promotions`).
- **Descuento (servidor, autoritativo): UNA sola unidad** de un producto elegible.
  `discount_cents = round(unit_price_cents × discount_percent / 100)`, acotado a
  `[0, subtotal]` (100% = una unidad gratis).
  - El cajero elige la unidad beneficiada vía `promotionProductId`.
  - Si se **omite** `promotionProductId`, el servidor toma el producto elegible (∈ promo)
    de **mayor precio** presente en el carrito.
  - Si la promo es elegible pero **ningún** producto suyo está en el carrito →
    `discount_cents = 0`, **no** se escribe historial ni se reinicia (se cobra normal).
- **Reinicio:** al aplicar (manual) una promoción con `resets_counter = true` que
  **efectivamente otorgó descuento (>0)**, tras incrementar el contador se escribe el
  snapshot en `loyalty_redemptions` y `visits → 0`. `visits_lifetime` no se toca.
- **Validación de promoción:** `name` **obligatorio** (no vacío; se muestra en el ticket);
  `discount_percent ∈ [1,100]`; `visit_threshold > 0`; **≥ 1 producto**; todos los
  productos del negocio.
- **Defaults aprobados:** baja de promo = archivar (`status='inactive'`); promos solapadas
  = el cajero elige (una por venta); sin tope de promos activas; un producto puede estar en
  varias promociones.

## 4. Contrato de API (requiere sesión; tenant-scoped)

### 4.1 CRUD de promociones — `/loyalty/promotions`

Forma `Promotion`:
```json
{
  "id": "uuid",
  "name": "50% café",
  "discountPercent": 50,
  "visitThreshold": 3,
  "resetsCounter": false,
  "status": "active",
  "productIds": ["uuid", "uuid"],
  "createdAt": "RFC3339",
  "updatedAt": "RFC3339"
}
```

| Método | Ruta | Cuerpo / Query | Respuesta |
|--------|------|----------------|-----------|
| GET | `/loyalty/promotions` | `?status=active\|inactive\|all` (def. active) | `{ "items": Promotion[] }` |
| POST | `/loyalty/promotions` | `{name, discountPercent, visitThreshold, resetsCounter, productIds[]}` | `201 { "promotion": Promotion }` |
| GET | `/loyalty/promotions/{id}` | — | `{ "promotion": Promotion }` |
| PUT | `/loyalty/promotions/{id}` | igual a POST | `{ "promotion": Promotion }` |
| DELETE | `/loyalty/promotions/{id}` | — | `204` — **soft delete** → `status='inactive'` (preserva historial) |

Errores: `validation_error` (400), `not_found` (404), `tenant_required` (400),
`unauthorized` (401). Reglas de validación en §3.

> **`/loyalty/config` (v1) se elimina.** La página `loyalty` deja de usar config;
> pasa a listar/editar promociones.

### 4.2 Elegibilidad del cliente (POS) — `GET /loyalty/customers/{customerId}/status`

Devuelve visitas y, por cada promoción **activa**, cuántas faltan y qué productos aplican.
Promociones ordenadas **ascendente por `visitsRemaining`** (las aplicables primero).
`visitsRemaining` y `applicableNow` se calculan con la fórmula de §3.1.

```json
{
  "customerId": "uuid",
  "visits": 2,
  "visitsLifetime": 9,
  "promotions": [
    {
      "promotionId": "uuid",
      "name": "50% café",
      "discountPercent": 50,
      "visitThreshold": 3,
      "resetsCounter": false,
      "visitsRemaining": 1,          // max(0, visitThreshold - visits)
      "redeemedThisCycle": false,    // ya canjeada en el ciclo actual (§3.1)
      "applicableNow": true,         // (visits + 1) >= visitThreshold  AND NOT redeemedThisCycle
      "products": [ { "id": "uuid", "name": "Café", "priceCents": 4500 } ]
    }
  ]
}
```
- `products` se devuelve siempre (para mostrar "para qué es" y para elegir la unidad
  beneficiada cuando `applicableNow=true`).
- Con `visits=2` el ejemplo da: promo umbral 3 → `visitsRemaining:1, applicableNow:true`;
  umbral 5 → `3,false`; umbral 7 → `5,false`.
- Errores: `not_found` (404) si el cliente no es del negocio.

### 4.3 Búsqueda de clientes — `GET /customers`

- **Nuevo:** `GET /customers?q=<texto>&limit=20` → busca por **nombre o teléfono**
  (`ILIKE '%q%'` sobre `first_name`, `last_name`, `phone`). Respuesta:
  `{ "items": Customer[] }` (lista, orden por nombre).
- **Se conserva:** `GET /customers?phone=<exacto>` → `{ "customer": Customer }` (404 si
  no existe) para lookup exacto/programático y retrocompatibilidad.
- `Customer` gana `visitsLifetime`:
```json
{ "id","tenantId","phone","firstName","lastName","visits","visitsLifetime","createdAt" }
```

### 4.4 Venta — `POST /sales`

Reemplaza `loyaltyReward` por `promotionId` (**a lo sumo uno** — una promoción por venta)
+ `promotionProductId` opcional (unidad beneficiada).

Request:
```json
{
  "items": [ { "productId": "uuid", "quantity": 2 } ],
  "paymentMethod": "cash|card",
  "amountPaidCents": 10000,
  "customerId": "uuid|null",
  "promotionId": "uuid|null",
  "promotionProductId": "uuid|null"
}
```
- `discountCents` **se elimina del request** (el servidor lo calcula). La respuesta `Sale`
  sigue exponiendo `discountCents` calculado y añade `promotionName` (para el ticket).
- `promotionProductId`: producto elegible del carrito que recibe el beneficio. Si es `null`
  (o inválido / no está en el carrito), el servidor toma el producto elegible de **mayor
  precio** presente en el carrito.
- Reglas de servidor (dentro de la transacción de venta, orden exacto):
  1. Validar cliente y productos; calcular subtotal con precios propios.
  2. Si `promotionId` ≠ null (requiere `customerId`):
     - la promoción debe ser del negocio, `status='active'` y **aplicable**:
       `customer.visits + 1 ≥ visit_threshold` **Y** no redimida en el ciclo actual
       (`redeemedThisCycle = false`, §3.1) → si no, `422 promotion_not_eligible`. El check
       de ciclo evita la doble redención por API dentro del mismo ciclo.
     - `chosen` = `promotionProductId` si es elegible y está en el carrito; si no, el
       producto elegible de mayor precio en el carrito.
     - Si `chosen` existe: `discount_cents = round(chosen.unit_price_cents ×
       discount_percent / 100)`, acotado a `[0, subtotal]`.
     - Si `chosen` no existe (ningún producto de la promo en el carrito):
       `discount_cents = 0` y la promoción se trata como **no aplicada** (sin historial,
       sin reinicio). Se cobra normal. *(No es error.)*
  3. Insertar `sales` (con `discount_cents`) y `sale_items`.
  4. `visits += 1`, `visits_lifetime += 1`.
  5. Si la promoción se aplicó (`discount_cents > 0`): insertar `loyalty_redemptions`
     (snapshot; `visits_cycle_at = visits` post-incremento;
     `visits_lifetime_at = visits_lifetime`; `caused_reset = resets_counter`;
     `discount_cents`). Si `resets_counter` → `visits = 0`.
  6. Commit.
- `Sale` en la respuesta: se **quita** `loyaltyReward`; se conserva `discountCents`; se
  añade `promotionName` (nombre de la promo aplicada, o `null`).
- Errores nuevos: `promotion_not_eligible` (422, umbral no alcanzado),
  `multiple_promotions` (400, si llegara más de una promoción).

## 5. Flujos de POS

### 5.1 Asociar cliente (modal)
- Campo de búsqueda único que consulta `GET /customers?q=` mientras se escribe
  (debounce) → lista de coincidencias por **nombre o teléfono**. Al tocar una, se asigna
  a la venta. Si no hay resultados → flujo de alta actual (`POST /customers`).
- Se conserva el atajo de búsqueda exacta por teléfono para el caso "ya sé el número".

### 5.2 Cliente ya asignado — 2 acciones
- **(1) Quitar:** desasocia el cliente (descarta cualquier promoción seleccionada).
- **(2) Ver detalle:** abre modal con `GET /loyalty/customers/{id}/status`:
  - "Lleva **N** visitas" (y opcionalmente "de por vida: M").
  - Lista de promociones **ascendente por `visitsRemaining`** (tramos de §3.1):
    - `visitsRemaining ≥ 2`: "Faltan **k** visitas para *{name}*" (informativo).
    - `visitsRemaining == 1`: "*{name}*: se desbloquea con esta compra" (seleccionable) +
      **productos** para elegir la unidad beneficiada.
    - `visitsRemaining ≤ 0`: "*{name}*: disponible" (seleccionable) + **productos**.
- El cajero **selecciona una** promoción `applicableNow` (o ninguna) y, si tiene varios
  productos elegibles, la **unidad** (`promotionProductId`). Selección = `promotionId`
  (+ `promotionProductId`) para el cobro. No se puede seleccionar más de una promoción.

### 5.3 Cobro
- `POST /sales` con `customerId` y el `promotionId` elegido (o null).
- El servidor calcula el descuento, registra la venta, incrementa contadores y —si
  corresponde— escribe el historial y reinicia. El ticket muestra el descuento aplicado.

## 6. Impacto en el código (guía para ingeniería, no implementación)

- **Backend** (`internal/loyalty`, `internal/customers`, `internal/sales`):
  - `loyalty`: nuevo `Promotion` (model), store CRUD, endpoint de `status` de cliente
    (elegibilidad). Eliminar `Config` y `/loyalty/config`.
  - `customers`: añadir `visitsLifetime` al modelo; `store.searchByQuery(q, limit)`;
    handler `?q=`.
  - `sales`: request pasa de `loyaltyReward`→`promotionId` (+ `promotionProductId`); quitar
    `discountCents` del request; el store calcula el descuento de **una unidad** desde la
    promoción, escribe `loyalty_redemptions` y aplica incremento/reinicio en la transacción.
    Quitar `loyalty_reward` del `Sale`; añadir `promotionName`.
- **Frontend** (`faro-ui`):
  - `lib/loyalty.ts`: reemplazar `LoyaltyConfig`/`eligibleReward` por API de promociones
    (`listPromotions`, `create/update/deletePromotion`) y `getCustomerLoyaltyStatus`.
  - `lib/customers.ts`: `searchCustomers(q)`; `Customer.visitsLifetime`.
  - `lib/sales.ts`: `createSale(..., { promotionId, promotionProductId })` (quitar
    `discountCents`/`loyaltyReward`); `Sale.promotionName`.
  - `app/(app)/loyalty/page.tsx`: pasar de config a **lista CRUD** de promociones.
  - `app/(app)/pos/page.tsx`: modal de cliente con búsqueda por nombre; acciones
    Quitar/Ver detalle; modal de detalle con orden ascendente por faltantes.

## 7. Criterios técnicos
- [ ] CRUD de promociones por negocio (solo productos propios; validaciones §3).
- [ ] `customers.visits_lifetime` añadido y backfilleado; `visits` como ciclo.
- [ ] Migración 0010 transforma la config v1 en promociones y elimina tablas v1
      (idempotente sobre la base viva; `down` documentado con pérdida de datos).
- [ ] Endpoint de elegibilidad con orden ascendente por faltantes y productos aplicables.
- [ ] Búsqueda de clientes por nombre y por teléfono.
- [ ] `POST /sales` con `promotionId` único; descuento calculado en servidor; historial
      y reinicio dentro de la transacción; aislamiento por negocio.
- [ ] Tests de integración verdes (incluye reinicio + snapshot + una-promo-por-venta).

## 8. Decisiones cerradas (antes preguntas abiertas)
Todas resueltas con el humano — **no quedan preguntas abiertas bloqueantes**:
1. Elegibilidad = **A**: la venta que completa el umbral cobra (`visits+1 ≥ umbral`);
   el modal muestra `faltan = umbral − visits` (§3.1).
2. Descuento = **una sola unidad** de un producto elegible; el cajero elige la unidad
   (`promotionProductId`), o el servidor toma la de mayor precio (§3.2).
3. `name` de promoción **obligatorio** y se muestra en el ticket.
4. Baja de promo = **archivar** (`status='inactive'`).
5. Promo aplicable pero sin sus productos en el carrito → **se cobra normal** sin descuento.
6. Promos solapadas → el **cajero elige** (una por venta).
7. **Sin tope** de promos activas; un producto puede estar en **varias** promociones.
