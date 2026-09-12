# Tech-spec — Repostería / Producción central (bakery)

_Autor: tech-lead · Fecha: 2026-08-13 · Módulo: M10 · Estado: **contratos definidos** (gate)_
_Fuentes: prd.md (aprobado) · brief.md · design/{research,wireframes,handoff}.md · diseño técnico aprobado (plan "Feature 2") · ADR-008 (almacén doble ledger) · ADR-010 (este módulo)_
_Consumido por: backend-engineer · frontend-engineer · devops-engineer_

> Esta spec define **el qué técnico** (modelo de datos, contratos de API, invariantes,
> decisiones abiertas D-A…D-D). El **cómo detallado** de cada dominio es del ingeniero
> respectivo. Decisión arquitectónica de fondo (stock de producto terminado + receta de
> doble semántica + transacción de producción) en **ADR-010**; el patrón de doble ledger
> heredado, en **ADR-008**.

---

## 0. Validación contra el código real y desviaciones del plan

Antes de formalizar, se validó el diseño aprobado contra el repo actual. **Desviaciones**
respecto del plan "Feature 2" que esta spec corrige:

1. **Numeración de migraciones.** El plan asumía Feature 1 en `0021` y Feature 2 en
   `0022`–`0026`. El repo tiene además `0020_warehouse_adjustment` (posterior al plan) y
   `0021_expenses_branch_optional` **ya en el working tree**. La siguiente libre es
   efectivamente **`0022`** → la numeración `0022`–`0026` del plan **se mantiene válida**.
2. **`warehouse_movements.type` ya incluye `adjustment`.** La migración `0020` amplió el
   CHECK a `('purchase','dispatch','waste','adjustment')`. El plan (escrito antes) asumía 3
   tipos. La migración de producción (§2.6) **extiende** el CHECK a **5** tipos
   (`… ,'adjustment','production'`), no a 4. Igual para el `.down.sql` (restaura los 4
   previos, no 3).
3. **Firma de `warehouse.Routes`.** Hoy es `Routes(requireSuperAdmin)` con
   `r.Use(requireSuperAdmin)` sobre **todas** las rutas, y `server.go` le pasa solo
   `authSvc.RequireSuperAdmin`. Para abrir `GET /warehouse/stock` a `repostero` (F17) hay que
   cambiar la firma a `Routes(requireSession, requireSuperAdmin)` y **dividir** el router
   (§7.2). Es un cambio de superficie en un módulo maduro; requiere regresión (§8).
4. **`deductSupplies` es set-based (una sola query con `unnest`+CTE).** El JOIN de
   `fulfillment_type` se agrega **dentro** de la CTE `consumo` (no es un `JOIN` externo
   trivial); ver §6.1 con el SQL exacto.
5. **`resolveScope` de insights rechaza todo rol ≠ super_admin/branch_admin.** Para incluir a
   `repostero` en la tendencia (F19) se necesita un **resolvedor propio** (`resolveBakeryScope`),
   no reusar `resolveScope` tal cual (§5.9).

Todo lo demás del diseño aprobado (modelo de pedidos/producciones, transacción central,
rol `repostero`, extensión de venta) calza con el código actual y se formaliza abajo.

---

## 1. Arquitectura del módulo

Nuevo paquete backend **`internal/bakery/`** (monolito modular, misma tríada que
`warehouse`/`supplies`: `model.go` / `store.go` / `service.go` / `handler.go` /
`response.go` + tests). Cablea en `cmd/api/main.go` (`bakery.NewService(pool)`) y en
`internal/server/server.go`:

```
r.Mount("/bakery", bakerySvc.Routes(authSvc.RequireSession))
```

Autorización **inline por rol en cada handler** (mismo estilo que `expenses`/`insights`, no
un middleware global): el módulo lo consumen 5 roles con permisos distintos (sucursal,
repostero, super_admin).

```
[ faro-ui /bakery/* ] ──HTTP/JSON (cookie)──► [ Go: internal/bakery ] ──► Postgres
   (gating por rol)                             reusa: products, product_supplies,
                                                warehouse_stock/movements (SQL directo),
                                                branches, users
[ faro-ui /insights ] ──► [ internal/insights +bakery-trend ] (extensión, §5.9)
[ POS venta ]         ──► [ internal/sales ] (bifurca por fulfillment_type, §6)
```

**Toca cinco módulos existentes** (por eso el gate de tech-spec, ver ADR-010):
`products` (F1/F2 + regla de cambio de tipo, §3.4), `sales` (§6), `warehouse` (§7.2),
`insights` (§5.9), `auth` (§7.1). El resto es nuevo en `internal/bakery`.

- **Reusa recetas, no duplica:** la receta de un postre **es** `product_supplies` tal cual,
  reinterpretada como "consumo por unidad producida" cuando `fulfillment_type='bakery'`
  (ADR-010 D2).
- **Reusa el almacén central único:** el consumo de insumos descuenta `warehouse_stock` /
  `warehouse_movements` (ADR-008); **no** hay segundo almacén.
- **Tercer dominio de stock:** producto terminado por sucursal (`product_branch_stock` +
  `product_stock_movements`), análogo a los dos de ADR-008 (ADR-010 D1).
- **Patrón transaccional:** cache + ledger **siempre en la misma tx**; nada bloqueante
  (stock negativo permitido, D-B). `SELECT FOR UPDATE` sobre el pedido serializa
  producciones concurrentes.

---

## 2. Modelo de datos

Cinco migraciones aditivas (`0022`–`0026`), convención de 0015-0021 (`IF NOT EXISTS`,
`BEGIN/COMMIT`, comentarios de intención, sin backfill de datos reales, invariante
`stock == SUM(movimientos)`).

### 2.1 `0022_auth_role_repostero` — rol nuevo (F20)

```sql
ALTER TABLE users DROP CONSTRAINT users_role_check;
ALTER TABLE users ADD CONSTRAINT users_role_check
    CHECK (role IN ('super_admin','branch_admin','cashier','barista','repostero'));
```

Sin backfill. `repostero` es **tenant-scoped** (a diferencia de `super_admin`, cuyo
`tenant_id` es NULL): lleva el `tenant_id` del negocio y **cero** membresías en
`user_branches` (F21). `down`: restaura el CHECK sin `repostero` (falla si existen filas
`repostero` — comportamiento correcto, no perder datos; documentar en el `.down.sql` como
0018/0019).

> Nota: el nombre de la constraint en 0013 es `users_role_check` (constraint inline sin
> nombre explícito → PostgreSQL la nombra `users_role_check`). Verificar con `\d users`
> antes de aplicar; si el nombre difiere, ajustar el `DROP CONSTRAINT`.

### 2.2 `0023_products_fulfillment_type` — tipo de producto (F1)

```sql
ALTER TABLE products
    ADD COLUMN fulfillment_type text NOT NULL DEFAULT 'branch_prepared'
    CHECK (fulfillment_type IN ('branch_prepared','bakery'));
```

Default `branch_prepared` preserva el comportamiento actual de todos los productos vivos
**sin backfill**. Comentario obligatorio en la migración (ADR-010 D2): la receta
(`product_supplies`) del producto se **reinterpreta** según este campo — consumo al vender
(`branch_prepared`) vs. al producir en la central (`bakery`). `down`: `DROP COLUMN`.

### 2.3 `0024_bakery_orders` — pedidos + producciones (F3-F14)

**`bakery_orders`** — un pedido de una sucursal a la repostería:

| Columna | Tipo | Notas |
|---|---|---|
| `id` | uuid PK | `gen_random_uuid()` |
| `tenant_id` | uuid NOT NULL FK tenants | scope |
| `branch_id` | uuid NOT NULL FK branches | **sin `ON DELETE`** (igual que `sales.branch_id`: no perder trazabilidad; la protección de borrado de sucursal sigue vía FK) |
| `product_id` | uuid NOT NULL FK products | postre pedido |
| `quantity_ordered` | integer NOT NULL | `CHECK (quantity_ordered > 0)` |
| `quantity_shipped` | integer NOT NULL DEFAULT 0 | `CHECK (quantity_shipped >= 0)`; acumulado despachado (F8) |
| `status` | text NOT NULL DEFAULT 'pending' | `CHECK (status IN ('pending','in_production','shipped','received','cancelled'))` |
| `note` | text NULL | opcional (F3) |
| `requested_by` | uuid NULL FK users | quién creó el pedido |
| `created_at` | timestamptz NOT NULL DEFAULT now() | |
| `updated_at` | timestamptz NOT NULL DEFAULT now() | avance de estado/surtido |

Índices: `(tenant_id, status, created_at)` (cola FIFO filtrada), `(tenant_id, branch_id, created_at)` (histórico de sucursal), `(product_id)`.

**`bakery_productions`** — cada acto de producción contra un pedido (F7, auditoría):

| Columna | Tipo | Notas |
|---|---|---|
| `id` | uuid PK | |
| `tenant_id` | uuid NOT NULL FK tenants | |
| `order_id` | uuid NOT NULL FK bakery_orders ON DELETE CASCADE | |
| `product_id` | uuid NOT NULL FK products | snapshot del postre producido |
| `branch_id` | uuid NOT NULL FK branches | snapshot de la sucursal destino (= `order.branch_id` al producir) |
| `quantity_produced` | integer NOT NULL | `CHECK (quantity_produced > 0)` |
| `created_by` | uuid NULL FK users | repostero/super_admin que registró |
| `created_at` | timestamptz NOT NULL DEFAULT now() | |

Índices: `(tenant_id, order_id, created_at)`, `(tenant_id, branch_id, product_id, created_at)` (auditoría por sucursal/postre).

### 2.4 `0025_product_stock` — stock de producto terminado (F15, F16 · ADR-010 D1)

**`product_branch_stock`** — cache de existencias de postre por sucursal:

| Columna | Tipo | Notas |
|---|---|---|
| `id` | uuid PK | |
| `tenant_id` | uuid NOT NULL FK tenants | |
| `product_id` | uuid NOT NULL FK products ON DELETE CASCADE | |
| `branch_id` | uuid NOT NULL FK branches | |
| `stock_qty` | integer NOT NULL DEFAULT 0 | **SIN CHECK de no-negatividad** (mismo criterio que `supply_branch_stock`/`warehouse_stock`, D-B) |
| `updated_at` | timestamptz NOT NULL DEFAULT now() | |
| | | `CONSTRAINT product_branch_stock_unique UNIQUE (product_id, branch_id)` |

Índice: `(tenant_id, branch_id)`. Fila **lazy**: se crea al primer `production_in`.

**`product_stock_movements`** — ledger firmado (fuente de verdad del stock de postre):

| Columna | Tipo | Notas |
|---|---|---|
| `id` | uuid PK | |
| `tenant_id` | uuid NOT NULL FK tenants | |
| `product_id` | uuid NOT NULL FK products ON DELETE CASCADE | |
| `branch_id` | uuid NOT NULL FK branches | |
| `type` | text NOT NULL | `CHECK (type IN ('production_in','sale','adjustment','waste'))` |
| `quantity` | integer NOT NULL | **FIRMADO**: `production_in (+)`, `sale (−)`, `adjustment (±)`, `waste (−)`; `CHECK (quantity <> 0)` |
| `bakery_production_id` | uuid NULL FK bakery_productions ON DELETE SET NULL | solo `production_in` |
| `sale_id` | uuid NULL FK sales ON DELETE SET NULL | solo `sale` |
| `reason` | text NULL | requerido en `waste`/`adjustment` (capa service; MVP no expone estos endpoints, ver nota) |
| `created_by` | uuid NULL FK users | NULL en descuento automático de venta |
| `created_at` | timestamptz NOT NULL DEFAULT now() | |

Invariante: `product_branch_stock.stock_qty == SUM(product_stock_movements.quantity)` por
(`product_id`, `branch_id`). Índices: `(tenant_id, product_id, branch_id, created_at)`,
`(sale_id)`, `(bakery_production_id)`.

> **Nota MVP:** solo se **escriben** `production_in` (producción) y `sale` (venta). Los tipos
> `adjustment`/`waste` se incluyen en el CHECK desde ahora (como 0015 incluyó `sale` antes de
> usarlo) para no re-migrar cuando se agregue el ajuste de stock de postre; **no** hay
> endpoint que los escriba en este MVP.

### 2.5 `0025` orden interno y `down`

Orden: `product_branch_stock` → `product_stock_movements`. `down`: drop ambas (inverso). La
FK de `product_stock_movements.bakery_production_id` apunta a `bakery_productions` (creada en
0024) → 0024 debe correr antes que 0025 (garantizado por el orden numérico del runner).

### 2.6 `0026_warehouse_production` — insumo consumido al producir (ADR-010 D3)

```sql
-- DESVIACIÓN DEL PLAN: el CHECK ya incluye 'adjustment' (migración 0020). Extender a 5.
ALTER TABLE warehouse_movements DROP CONSTRAINT warehouse_movements_type_check;
ALTER TABLE warehouse_movements ADD CONSTRAINT warehouse_movements_type_check
    CHECK (type IN ('purchase','dispatch','waste','adjustment','production'));

-- Trazabilidad: liga el consumo de insumo con la producción que lo causó.
ALTER TABLE warehouse_movements
    ADD COLUMN IF NOT EXISTS bakery_production_id uuid NULL
    REFERENCES bakery_productions(id) ON DELETE SET NULL;
```

`type='production'`: `quantity_base` negativo, `branch_id` **NULL** (el insumo se consume
centralmente, no se despacha a una sucursal — se distingue así de `dispatch`), y
`bakery_production_id` set. `down`: drop columna FK, restaurar CHECK a los 4 previos
(`… ,'adjustment'`) — falla si hay filas `production` (correcto; documentar).

**Orden global de migraciones:** `0022` (rol) → `0023` (fulfillment_type) → `0024`
(orders/productions) → `0025` (product stock) → `0026` (warehouse production, su FK apunta a
`bakery_productions` de 0024).

---

## 3. Resolución de las dudas abiertas de diseño (handoff §6)

### 3.1 D-A — Umbral de antigüedad (aging): **hardcodeado en el frontend, 2 días**

El aging es un **indicador puramente visual** que no afecta datos ni lógica de negocio
(handoff §6, research D4). Decisión: **no** es configurable por tenant en el MVP; vive como
constante en el frontend, no en el backend ni en la BD.

- El backend ya expone `createdAt` de cada pedido (necesario para la columna Fecha); **no**
  se agrega ningún campo `ageDays` ni umbral al contrato de API.
- La constante vive en **`faro-ui/lib/bakery.ts`**: `export const AGING_THRESHOLD_DAYS = 2;`.
  El frontend calcula `ageDays = floor((now − createdAt)/1d)` y pinta el indicador
  (`pending`/`in_production` con `ageDays >= AGING_THRESHOLD_DAYS`).

**Rationale:** hacerlo tenant-configurable agrega superficie de settings con cero valor MVP;
es una heurística de priorización, fácil de cambiar en un punto. Consistente con el
precedente de umbrales-como-constante-documentada de insights (`defaultSecondVisitSampleMin`,
etc.). Si el negocio pide ajustarlo, se mueve a `settings` sin romper contrato.

### 3.2 D-B — Stock insuficiente al producir: **permitido, no bloqueante** (confirma R6/D10)

Se **confirma** el criterio de warehouse: producir sin insumos suficientes **se registra
igual** y deja `warehouse_stock.stock_base` negativo. Sin CHECK de no-negatividad en ninguno
de los tres dominios de stock. El backend **no rechaza**; el frontend **avisa** (`Alert
warning` no bloqueante, D10).

**No rompe ninguna invariante:** las invariantes son `stock == SUM(movimientos)`, y se
mantienen exactas con stock negativo (el negativo es un valor legítimo de la suma). El único
efecto que agrega positivo al stock de postre es `production_in`; los negativos del almacén
vienen del consumo, coherente con `dispatch`/`sale` ya en producción. No hay un camino donde
un negativo corrompa otra cosa.

### 3.3 D-C — Cambio de `fulfillment_type` con pedidos/stock existentes: **bloqueo condicional**

Regla en `products.Update` (validada por SQL directo contra `bakery_orders` y
`product_branch_stock`, mismo criterio cross-módulo que `sales`→`supplies`):

- **`branch_prepared → bakery`:** **permitido siempre.** Un producto que no era de repostería
  no puede tener `bakery_orders` (nunca se pudo pedir) ni `product_branch_stock`. Gana el
  flujo de repostería limpio.
- **`bakery → branch_prepared`:** **bloqueado** (409 `fulfillment_change_blocked`) si el
  producto tiene **cualquiera** de:
  - pedidos **abiertos** (`status IN ('pending','in_production')`), o
  - `product_branch_stock` con `stock_qty <> 0` en alguna sucursal.

  **Permitido** solo cuando no hay pedidos abiertos **y** todo el stock de postre está en 0 (o
  no existe). Los pedidos **terminales** (`shipped`/`received`/`cancelled`) **no** bloquean
  (son histórico inmutable).

**Rationale:** cambiar a `branch_prepared` con pedidos abiertos los dejaría **inproducibles**
(la transacción de producción exige `fulfillment_type='bakery'`, §4). Con stock terminado
> 0, la venta pasaría a descontar **insumos** (vía receta) en vez del stock de postre ya
acreditado (D4) → **doble contabilidad silenciosa** y stock de postre huérfano. Bloquear es
la única opción que preserva las invariantes sin migración de datos ni comportamiento
ambiguo. El error 409 devuelve el **conteo de bloqueadores** para que la UI explique
(`{ "code":"fulfillment_change_blocked", "openOrders": n, "branchesWithStock": m }`).

### 3.4 D-D — `GET /bakery/stock` "todas las sucursales": **una fila por (producto, sucursal)**

Contrato único parametrizado por scope (rol):

- **Sucursal** (branch_admin/cashier/barista): el servidor fuerza `branchId = activeBranch`;
  devuelve solo filas de su sucursal.
- **Repostero / super_admin:** todas las sucursales; `?branchId=<uuid>` opcional filtra a
  una.

La respuesta es una **lista plana, un renglón por (producto, sucursal)** — no un resumen
agregado por producto con desglose. Rationale: es la forma operativamente útil ("¿cuánto
postre X hay en la sucursal Y?"), calza con la "columna extra Sucursal" del wireframe (§2.2
del handoff), y el cliente puede agrupar/sumar por producto en memoria si quiere. Evita un
contrato con dos formas (agregado vs. detalle) y su ambigüedad. Solo se listan filas con
producto `fulfillment_type='bakery'`; `stock_qty` puede ser 0 (se muestra en `text-muted`) o
negativo.

```json
{
  "scope": "all" | "branch",
  "items": [
    { "productId":"…", "productName":"Cheesecake", "branchId":"…",
      "branchName":"Centro", "stockQty": 12 }
  ]
}
```

Orden: `productName, branchName`. Para `scope:"branch"`, `items` trae solo la sucursal
activa (con su `branchId`/`branchName`).

---

## 4. Transacción central: `POST /bakery/orders/{id}/produce` (F7-F11 · ADR-010 D3)

Réplica de `warehouse.insertDispatch`. **Una sola transacción** (`s.pool.Begin` →
`defer tx.Rollback` → `tx.Commit`), rollback total ante cualquier fallo:

1. **`SELECT … FOR UPDATE`** sobre `bakery_orders WHERE id=$1 AND tenant_id=$2` — serializa
   producciones concurrentes contra el mismo pedido (R3). Si no existe / otro tenant →
   `ErrNotFound` (404).
2. **Validar:**
   - `status IN ('pending','in_production')` → si no, **409 `invalid_state`**.
   - el producto sigue `fulfillment_type='bakery'` y `status='active'` → si no, **409
     `invalid_state`** (defensivo: el bloqueo de §3.3 lo previene, pero se revalida bajo
     lock).
   - `quantityProduced > 0` → si no, **400 `validation_error`**.
3. **`INSERT bakery_productions`** (`order_id`, `product_id`, `branch_id = order.branch_id`,
   `quantity_produced`, `created_by`) → `productionID`.
4. **Cargar receta** (`product_supplies` del producto). Sin filas = **no-op natural** (postre
   sin receta no descuenta insumos; mismo criterio que `deductSupplies`).
5. **Por cada insumo (ORDER BY `supply_id`, anti-deadlock):**
   - `INSERT warehouse_movements(type='production', quantity_base = −(ps.quantity_base ×
     quantityProduced), branch_id NULL, bakery_production_id=productionID, created_by)`.
   - `UPSERT warehouse_stock` `stock_base −= consumo` (`ON CONFLICT (supply_id)`).
   Set-based con `unnest`+CTE igual que `deductSupplies` (una query), para orden de bloqueo
   determinista.
6. **Acreditar postre:**
   - `INSERT product_stock_movements(type='production_in', quantity = +quantityProduced,
     branch_id=order.branch_id, bakery_production_id=productionID, created_by)`.
   - `UPSERT product_branch_stock` `stock_qty += quantityProduced`
     (`ON CONFLICT (product_id, branch_id)`).
7. **`UPDATE bakery_orders`** `SET quantity_shipped = quantity_shipped + quantityProduced,
   status = CASE WHEN quantity_shipped + quantityProduced >= quantity_ordered THEN 'shipped'
   ELSE 'in_production' END, updated_at = now()`.

**Parcialidad (F8/F9):** se permite `quantityProduced` menor, igual o **mayor** que lo que
falta (el diseño §2.4 permite `> falta`). El estado cierra en `shipped` cuando el acumulado
`>= quantity_ordered`. Se pueden registrar varias producciones hasta cerrar.

Devuelve: el pedido actualizado (con `quantityShipped`, `status`), y opcionalmente el
`warehouseStock`/`productStock` resultantes (para refrescar la fila). Ver §5.5.

---

## 5. Contratos de API (`internal/bakery`, montado en `/bakery`)

Router bajo `RequireSession`; **autorización inline por rol** en cada handler. Envelopes
como el resto: `{ "items": [...] }` para listas, `{ "<recurso>": {...} }` singular. Errores
con `writeError(code, mensaje)`: `validation_error`, `not_found`, `invalid_branch`,
`invalid_product`, `invalid_state`, `forbidden`, `conflict`, `internal`. Tenant vía
`auth.ResolveTenant` (funciona para sucursal, repostero y super_admin; §7.1).

Helpers de rol (en `handler.go`, leídos de `auth.UserFromContext`):
- `isBranchUser` = role ∈ {branch_admin, cashier, barista} (tiene `activeBranch`).
- `isProduction` = `u.IsSuperAdmin || u.Role == 'repostero'` (ve todas las sucursales).

### 5.1 Crear pedido — F3, F4

`POST /bakery/orders` — **solo sucursal** (`isBranchUser`; super_admin/repostero → 403
`forbidden`, no tienen sucursal a nombre de quién pedir).

Request:
```json
{ "productId": "…", "quantity": 10, "note": "para el finde" }
```
- `branchId` **no** viene en el body: se toma de `auth.ActiveBranchFromContext` (F3). Si el
  usuario no tiene sucursal activa → 400 `invalid_branch`.
- `productId` debe existir, ser del tenant, `status='active'` y `fulfillment_type='bakery'`
  (D1) → si no, 400 `invalid_product`.
- `quantity > 0` → si no, 400 `validation_error`. `note` opcional.

Response `201`:
```json
{ "order": { "id":"…","branchId":"…","branchName":"Centro","productId":"…",
  "productName":"Cheesecake","quantityOrdered":10,"quantityShipped":0,
  "status":"pending","note":"para el finde","requestedByName":"Ana","createdAt":"…" } }
```

### 5.2 Listar pedidos / cola — F5, F6

`GET /bakery/orders` — scope por rol:
- **Sucursal:** solo pedidos de su `activeBranch` (histórico propio, F6).
- **Repostero / super_admin:** todos los pedidos, todas las sucursales (cola, F5).

Query params (todos opcionales): `?status=pending,in_production` (CSV; default sin filtro),
`?branchId=<uuid>` (ignorado para usuarios de sucursal), `?q=<texto>` (nombre de postre).
Orden: **`created_at ASC` (FIFO)** — el más antiguo primero (prioridad de la cola, D4).

Response: `{ "items": [ <order con los mismos campos que 5.1> ] }`. Cada item incluye
`quantityShipped` (surtido acumulado) para que el front pinte `n/m` + barra y "Falta" =
`quantityOrdered − quantityShipped` (§2.3 handoff). `createdAt` alimenta el aging (D-A).

### 5.3 Detalle de pedido + producciones

`GET /bakery/orders/{id}` — dueña (su sucursal) / repostero / super_admin (403 si un usuario
de sucursal pide uno de otra sucursal). Response:
```json
{ "order": { … },
  "productions": [ { "id":"…","quantityProduced":6,"createdByName":"Repo","createdAt":"…" } ] }
```

### 5.4 Cancelar — F12, F13

`PATCH /bakery/orders/{id}/cancel` — dueña (su sucursal) / super_admin. Bajo `FOR UPDATE`:
solo si `status='pending'` **y** `quantity_shipped = 0` (nada producido) → `status='cancelled'`.
Cualquier otro estado → **409 `invalid_state`**. Response `{ "order": { … } }`.

> Nota: por diseño `quantity_shipped>0` implica `status ∈ {in_production, shipped}`, así que
> el chequeo de estado ya cubre "nada producido"; se valida `quantity_shipped=0` igual como
> segunda barrera (F12 explícito).

### 5.5 Registrar producción — F7-F11 (§4)

`POST /bakery/orders/{id}/produce` — **solo `isProduction`** (repostero/super_admin; sucursal
→ 403). Request:
```json
{ "quantity": 6 }
```
`quantity > 0`. Ejecuta la transacción §4. Response `200`:
```json
{ "order": { …, "quantityShipped":6, "status":"in_production" },
  "production": { "id":"…","quantityProduced":6,"createdAt":"…" } }
```
Errores: 404 `not_found` (pedido ajeno/inexistente), 409 `invalid_state` (pedido ya cerrado/
cancelado o producto ya no es bakery), 400 `validation_error`.

### 5.6 Marcar recibido — F14 (informativo, ADR-010 D5)

`PATCH /bakery/orders/{id}/receive` — **solo dueña** (usuario de la sucursal del pedido;
super_admin permitido por operar todo, repostero → 403). Solo si `status='shipped'` →
`status='received'`. **No mueve stock.** Otro estado → 409 `invalid_state`.

### 5.7 Stock de postres — F15 (§3.4 D-D)

`GET /bakery/stock` — sucursal (la suya) / repostero+super_admin (todas, `?branchId`
opcional, `?q` opcional). Contrato exacto en §3.4.

### 5.8 Auditoría de producciones — (RNF auditabilidad)

`GET /bakery/productions` — **solo `isProduction`**. `?from&to` (RFC3339) `?branchId`
`?productId` opcionales, LIMIT 100 DESC. Response `{ "items": [ { id, orderId, productName,
branchName, quantityProduced, createdByName, createdAt } ] }`.

### 5.9 Tendencia de venta — F18, F19 (extensión de `internal/insights`)

`GET /insights/bakery-trend` — **nuevo handler en `insights`** (no en `bakery`, es una
extensión de Insights). Resolvedor de scope **propio** (`resolveBakeryScope`, no reusa
`resolveScope` que rechaza a `repostero`):
- **super_admin:** todas; `?branchId=<uuid>` filtra, `?branchId=none` bucket sin sucursal.
- **branch_admin:** forzado a su `activeBranch`; ignora `?branchId`.
- **repostero:** todas (sin sucursal activa); `?branchId` opcional.
- **cashier / barista:** **403 `forbidden`**.

Agregación **SQL pura, sin IA** (ADR-009): sobre `sales`/`sale_items` JOIN `products` con
`fulfillment_type='bakery'`, compara **semana en curso vs. anterior** por postre (y por
sucursal según scope). Ventanas calculadas con el `tz` (offset en minutos, como el resto de
insights). Response:
```json
{ "weekCurrent": {"from":"…","to":"…"}, "weekPrevious": {"from":"…","to":"…"},
  "items": [ { "productName":"Cheesecake", "unitsPrevious":8, "unitsCurrent":14,
               "deltaUnits":6, "deltaPct":75.0 } ] }
```
`deltaPct` omitido/`null` si `unitsPrevious=0` (guarda de división, patrón insights). Orden:
**`deltaUnits DESC`** (D6: qué reforzar = lo que más creció en volumen). Vacío si no hay
ventas de postres en las dos semanas.

---

## 6. Impacto en la venta (`internal/sales/store.go`) — F16, R1 (ADR-010 D4)

### 6.1 `deductSupplies`: no descontar insumos de postres

En la CTE `consumo` de la query set-based existente (§ leída, líneas ~279-285), añadir el
filtro por tipo para que las líneas de postre **no** generen consumo de insumos:
```sql
consumo AS (
    SELECT ps.supply_id, SUM(ps.quantity_base * l.qty)::int AS total
      FROM lineas l
      JOIN products p ON p.id = l.product_id AND p.tenant_id = $1
                     AND p.fulfillment_type = 'branch_prepared'
      JOIN product_supplies ps ON ps.product_id = l.product_id AND ps.tenant_id = $1
  GROUP BY ps.supply_id
)
```
Un postre (`bakery`) no entra en `consumo` → no escribe `supply_movements('sale')` ni toca
`supply_branch_stock`. Sin cambios en la firma ni en el resto de `CreateSale`.

### 6.2 `deductFinishedGoods` (nueva): descontar stock de postre

Nueva función con la **misma estructura** que `deductSupplies` (set-based, `unnest`+CTE,
`ORDER BY product_id` anti-deadlock), llamada en `CreateSale` **justo después** de
`deductSupplies` (dentro de la misma tx de la venta). Filtra líneas con
`fulfillment_type='bakery'`, escribe `product_stock_movements(type='sale', quantity =
−qty, sale_id, branch_id = sale.BranchID, created_by NULL)` y hace upsert restando en
`product_branch_stock` (`ON CONFLICT (product_id, branch_id)`). No bloqueante (stock de
postre puede quedar negativo, D-B). Si `sale.BranchID` es nil → no-op (defensivo).

**Carrito mixto (F16):** café (`branch_prepared`) pasa solo por `deductSupplies`; postre
(`bakery`) solo por `deductFinishedGoods`. Cada función filtra por tipo en su JOIN → sin
solapamiento ni doble descuento.

---

## 7. Wiring de autorización por rol

### 7.1 Rol `repostero` (`internal/auth/`)

- **`validate.go`:** agregar `RoleRepostero = "repostero"` a las constantes y a `validRole`.
  **No** agregarlo a `isBranchRole` (no exige ni admite sucursal — F21).
- **`service_provision.go CreateUser`:** nueva rama para `repostero` — exige `branchIDs`
  **vacío** (si trae alguna → `ErrValidation`) y crea con **`tenant_id` del negocio** (el
  `tenantID` que ya recibe la función), a diferencia de `super_admin` (tenant NULL). Requiere
  un método de store `createReposteroUser(ctx, tenantID, email, name, hash)` (análogo a
  `createSuperAdminUser` pero con `tenant_id` set, `role='repostero'`, `is_super_admin=false`,
  sin membresías).
- **`service_provision.go UpdateUser` — blindaje del invariante (R4):** hoy `patch.BranchIDs`
  **no** valida el rol del usuario objetivo. Agregar: **cargar el rol actual del usuario
  objetivo**; si es `repostero`, **rechazar** cualquier `patch.BranchIDs` no vacío
  (`ErrValidation`) y rechazar `patch.Role` que lo saque de `repostero` o lo convierta a un
  rol de sucursal (fuera de alcance del PATCH; `ErrValidation`). El guard existente de
  `patch.Role` (solo `isBranchRole`) ya impide *promover* a `repostero` por esta vía; falta
  el lado de *no asignarle sucursales*. Sin este blindaje, el formulario de edición podría
  colgarle una sucursal a un repostero.
- **Login/JWT/middleware: sin cambios.** Un usuario con 0 membresías resuelve
  `activeBranch=nil`, `mustSelectBranch=false` de forma natural (mismo camino que
  super_admin). `ResolveTenant` funciona: `repostero` tiene `tenant_id` propio → devuelve ese
  tenant sin pasar por `businessTenantID`.

### 7.2 Apertura de `GET /warehouse/stock` a `repostero` (F17, R5)

Cambio **quirúrgico y acotado** en `internal/warehouse/handler.go` `Routes` y en
`internal/server/server.go`:

- **Firma:** `Routes(requireSession, requireSuperAdmin func(http.Handler) http.Handler)`.
- **Split del router:** `GET /stock` va en un grupo bajo `requireSession` con **autorización
  inline** (`u.IsSuperAdmin || u.Role == 'repostero'`, si no → 403). **Todo lo demás**
  (`/stock/{id}` PATCH, `/stock/{id}/adjust`, `/to-buy`, `/suppliers*`, `/purchases*`,
  `/dispatches*`, `/waste*`) permanece bajo `requireSuperAdmin`. Patrón: dos subgrupos chi
  (`r.Group(func(r){ r.Use(requireSession); r.Get("/stock", …) })` y
  `r.Group(func(r){ r.Use(requireSuperAdmin); … })`).
- **`server.go`:** `warehouseSvc.Routes(authSvc.RequireSession, authSvc.RequireSuperAdmin)`.
- El handler `handleListStock` no cambia su lógica; solo antepone el chequeo inline de rol.
  `ResolveTenant` ya funciona para `repostero`.

**Regresión obligatoria (§8):** branch_admin/cashier/barista → **403** en `GET
/warehouse/stock` y en **todas** las demás rutas de `/warehouse`; repostero → **403** en todo
`/warehouse` **excepto** `GET /stock`.

### 7.3 `fulfillment_type` en `products` (F1, F2, §3.3)

- **`handler.go`:** `createRequest` y `updateRequest` ganan `FulfillmentType *string
  json:"fulfillmentType"`. Solo se acepta si el caller es super_admin (los endpoints ya son
  `requireSuperAdmin` para escritura → gating cubierto); valor ∈ {`branch_prepared`,`bakery`}
  o `ErrValidation`.
- **`service.go Create`:** default `branch_prepared` si ausente.
- **`service.go Update`:** aplica la **regla D-C (§3.3)** — si el cambio es
  `bakery → branch_prepared`, consultar (SQL directo) pedidos abiertos y stock de postre; si
  hay bloqueadores → `ErrFulfillmentBlocked` (nuevo, → 409 `fulfillment_change_blocked` con
  conteos). `store` incluye `fulfillment_type` en el `SELECT`/`INSERT`/`UPDATE` de products y
  en el `Product` model (`FulfillmentType string json:"fulfillmentType"`).

### 7.4 Gating por rol — resumen de endpoints

| Endpoint | branch (admin/cashier/barista) | repostero | super_admin |
|---|---|---|---|
| `POST /bakery/orders` | ✅ (su sucursal) | ❌ 403 | ❌ 403 |
| `GET /bakery/orders` | ✅ (solo suyos) | ✅ (todos) | ✅ (todos) |
| `GET /bakery/orders/{id}` | ✅ (si es suyo) | ✅ | ✅ |
| `POST /bakery/orders/{id}/produce` | ❌ 403 | ✅ | ✅ |
| `PATCH …/cancel` | ✅ (dueña) | ❌ 403 | ✅ |
| `PATCH …/receive` | ✅ (dueña) | ❌ 403 | ✅ |
| `GET /bakery/stock` | ✅ (la suya) | ✅ (todas) | ✅ (todas) |
| `GET /bakery/productions` | ❌ 403 | ✅ | ✅ |
| `GET /insights/bakery-trend` | branch_admin ✅ (la suya) · cashier/barista ❌ | ✅ (todas) | ✅ (todas) |
| `GET /warehouse/stock` | ❌ 403 | ✅ | ✅ |
| resto de `/warehouse` | ❌ 403 | ❌ 403 | ✅ |
| `PATCH /products/{id}` (fulfillmentType) | ❌ 403 | ❌ 403 | ✅ |

---

## 8. Plan de pruebas (alto nivel)

**Migraciones**
- `0022`–`0026` up sobre BD con datos vivos de products/sales/warehouse: no rompen filas;
  `fulfillment_type` de productos existentes queda `branch_prepared`; el CHECK de
  `warehouse_movements` admite `production` **conservando** `adjustment` (regresión de la
  desviación §0.2). `down` revierte (falla esperada si hay filas de los tipos nuevos).

**Ciclo de vida del pedido (integración crítica)**
- Crear pedido (sucursal) → `pending`, aparece en su histórico y en la cola.
- Producción parcial (6 de 10) → `in_production`, `quantityShipped=6`; segunda producción (4)
  → `shipped`. Verificar en cada paso: `warehouse_stock` bajó según receta × producido,
  `product_branch_stock` de la sucursal subió el producido, ambos ledgers cuadran con su
  cache (invariantes).
- Producción `> falta` permitida; cierra en `shipped`.
- No se puede producir sobre `cancelled`/`shipped`/`received` → 409 `invalid_state`.
- Postre **sin receta**: producción acredita el postre y **no** escribe movimientos de
  almacén (no-op natural).

**Cancelar / recibir**
- Cancelar solo `pending` sin producción; `in_production`/`shipped` → 409. `received` solo
  desde `shipped`; no altera `product_branch_stock`.

**Doble efecto / no doble contabilidad (R1)**
- Vender un postre descuenta `product_branch_stock` y **no** toca insumos.
- Vender un `branch_prepared` descuenta insumos y **no** toca `product_branch_stock`.
- Carrito mixto (café + postre): cada uno descuenta de su dominio, sin solapar.

**Concurrencia (R3)**
- Dos `POST …/produce` concurrentes contra el mismo pedido: `FOR UPDATE` serializa;
  `quantity_shipped` final = suma correcta, sin estado corrupto ni sobre-cierre.

**Autorización (R4, R5)**
- Regresión warehouse: branch_admin/cashier/barista → 403 en **todo** `/warehouse`;
  repostero → 403 en todo `/warehouse` salvo `GET /stock` (200).
- Gating bakery: cada fila de la tabla §7.4 (sucursal no puede producir; repostero no puede
  crear pedido ni cancelar/recibir; cashier/barista sin trend).
- Invariante repostero-sin-sucursal: `CreateUser` con `branchIDs` no vacío → error;
  `UpdateUser` con `branchIDs` sobre un repostero → error; login de repostero aterriza sin
  `select-branch` (E2E).

**Cambio de `fulfillment_type` (D-C)**
- `branch_prepared → bakery`: siempre OK.
- `bakery → branch_prepared` con pedido abierto → 409; con `product_branch_stock>0` → 409;
  limpio (sin abiertos, stock 0) → OK.

**Tendencia (F18/F19)**
- Agregación correcta semana vs. semana por postre/sucursal; scope por rol (repostero todas,
  branch_admin la suya, cashier/barista 403); `deltaPct` null si base 0; vacío sin ventas.

**Aislamiento por tenant** en las 4 tablas nuevas.

> Gotcha del proyecto: verificar `TEST_DATABASE_URL=faro_test` antes de `go test` (memoria).

---

## 9. Contrato con frontend (resumen para frontend-engineer)

- `lib/bakery.ts` nuevo (estilo `lib/warehouse.ts`) con las llamadas de §5 y la constante
  `AGING_THRESHOLD_DAYS = 2` (D-A). `lib/auth.ts`: `Role` gana `'repostero'`; landing de
  repostero → `/bakery/queue`.
- Pantallas: `/bakery/orders` (sucursal), `/bakery/stock` (sucursal + all), `/bakery/queue`
  (producción, con modal §2.4), `/bakery/supplies` (repostero, vía `GET /warehouse/stock`),
  `/bakery/trend` + sección en Insights. Selector `fulfillmentType` en `/products/[id]/edit`
  y `/products/new` (solo super_admin) + relabel de la receta.
- Progreso `n/m` + barra desde `quantityShipped`/`quantityOrdered`; "Falta" = resta. Aging
  desde `createdAt` (front). Estado con `StatusBadge` (agregar variante `success`, handoff
  §4.3).
- 409 `invalid_state` en produce/cancel/receive → mensaje claro + refrescar la fila (el
  estado cambió por debajo, concurrencia). 409 `fulfillment_change_blocked` → explicar con
  `openOrders`/`branchesWithStock`.
- `stockQty` de postre puede ser 0/negativo; 0 en `text-muted`.

## 10. Contrato con devops (resumen)

- Sin infra nueva, sin variables de entorno, sin dependencias externas. Cinco migraciones
  `0022`–`0026` (up/down) por el runner existente, en orden.
- Migraciones que alteran CHECK de tablas en producción (`users`, `warehouse_movements`):
  rápidas (metadata, sin reescritura de filas) pero aplicar en la ventana de deploy estándar.
  `down` documentado (fallo esperado si ya hay filas de los tipos/roles nuevos).
- Validar en local (LAN) antes de prod (regla del proyecto).

## 11. Riesgos y trade-offs

| # | Riesgo | Mitigación |
|---|---|---|
| R1 | Doble contabilidad de insumos (postre descuenta al producir y al vender) | Bifurcación por `fulfillment_type` en `deductSupplies` + `deductFinishedGoods` (§6); tests de carrito mixto (§8). |
| R2 | Stock de producto terminado es un dominio nuevo que convive con la venta actual | Ledger + cache con invariante homogénea a ADR-008; solo postres tienen filas; `branch_prepared` intacto. |
| R3 | Concurrencia en `/produce` corrompe `quantity_shipped`/estado | `SELECT FOR UPDATE` sobre el pedido; test de concurrencia. |
| R4 | Se le asigna sucursal a un repostero (invariante roto) | Blindaje en `CreateUser` **y** `UpdateUser` (§7.1); test explícito. |
| R5 | Abrir lectura de almacén a repostero expone el resto | Split del router: solo `GET /stock` con auth inline; resto sigue super_admin; regresión de los 3 roles de sucursal (§8). |
| R6 | Stock negativo al producir sin insumos | Confirmado no bloqueante (D-B), consistente con warehouse; aviso soft en UI. |
| R7 (nuevo) | Cambiar `fulfillment_type` deja pedidos inproducibles / stock huérfano | Bloqueo condicional D-C (§3.3) con 409 + conteos; test de los tres casos. |
| R8 (nuevo, deuda) | `warehouse.Routes` y `sales`/`products` (módulos maduros) cambian de superficie | Cambios acotados y documentados; batería de regresión (§8). Deuda: la doble semántica de `product_supplies` debe quedar comentada fuerte en 0023 y en el código (ADR-010 D2). |

## 12. Notas abiertas

Ninguna que bloquee task-breakdown. Las 4 dudas de diseño (handoff §6) quedan resueltas en
§3 (D-A…D-D). `adjustment`/`waste` de `product_stock_movements` quedan en el CHECK pero **sin
endpoint** en el MVP (§2.4 nota): futura corrección manual de stock de postre.
</content>
