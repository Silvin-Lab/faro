# Tech-spec — Almacén (warehouse)

_Autor: tech-lead · Fecha: 2026-07-20 · Módulo: M8 · Estado: **contratos definidos** (gate)_
_Fuentes: prd.md (aprobado) · design/handoff.md · design/wireframes.md · 0015_supplies · ADR-008_
_Consumido por: backend-engineer · frontend-engineer · devops-engineer_

> Esta spec define **el qué técnico** (modelo de datos, contratos de API, invariantes,
> decisiones R1-R4). El **cómo detallado** de cada dominio es del ingeniero respectivo.
> Decisión arquitectónica de fondo (doble ledger + `transfer`) en **ADR-008**.

---

## 1. Arquitectura del módulo

Nuevo módulo backend `internal/warehouse/` (monolito modular, mismo patrón que
`internal/supplies`), montado en **`/warehouse`**, con la tríada `handler.go` / `service.go`
/ `store.go` / `model.go`. Se cablea en `cmd/api/main.go` (`warehouse.NewService(pool)`) y
`internal/server/server.go` (`r.Mount("/warehouse", warehouseSvc.Routes(...))`).

```
[ faro-ui /warehouse/* ]  ──HTTP/JSON (cookie)──►  [ Go: internal/warehouse ]  ──►  Postgres
   (gated a super_admin)                              reusa supplies (catálogo),
                                                      branches, users
```

- **Reusa, no duplica, el catálogo:** los ítems del almacén **son** los `supplies`
  existentes (F2). El módulo no tiene catálogo propio; referencia `supplies(id)`.
- **Dos dominios de stock separados** (ADR-008): almacén central (`warehouse_stock` +
  ledger `warehouse_movements`) y sucursal (`supply_branch_stock` + `supply_movements`, ya
  existentes). Solo la **salida (dispatch)** cruza ambos, en una transacción.
- **Gating (handoff §1.2):** **todo** el módulo (lectura y escritura) requiere
  `super_admin` — Almacén está gated como `supplies` de escritura. Se usa
  `authSvc.RequireSuperAdmin` en todas las rutas.
- **Patrón transaccional:** idéntico a `insertMovement`/`deductSupplies` — el cache de
  stock y el ledger se escriben **siempre en la misma transacción** (sin ventana de
  inconsistencia). Nada es bloqueante (ver R4).

---

## 2. Modelo de datos (migración `0019_warehouse`)

Tres tablas nuevas + una extensión de `supply_movements`. Convención y comentarios como
0015-0018 (aditiva, no destructiva, `IF NOT EXISTS`, `BEGIN/COMMIT`).

### 2.1 `suppliers` — catálogo de proveedores (F6)

| Columna | Tipo | Notas |
|---|---|---|
| `id` | uuid PK | `gen_random_uuid()` |
| `tenant_id` | uuid NOT NULL FK tenants | scope |
| `name` | text NOT NULL | `UNIQUE(tenant_id, name)` |
| `address` | text NULL | opcional |
| `email` | text NULL | opcional |
| `phone` | text NULL | opcional |
| `status` | text NOT NULL DEFAULT 'active' | `CHECK (status IN ('active','inactive'))` — baja **soft** |
| `created_at` | timestamptz NOT NULL DEFAULT now() | |

Baja soft vía `status='inactive'` (el handoff §2.4 acepta soft o hard; elijo **soft** por
consistencia con `supply_categories`/`supplies` y para no romper el historial de compras que
referencia al proveedor).

### 2.2 `warehouse_stock` — cache de existencias del almacén + mín/máx (F1-F4)

| Columna | Tipo | Notas |
|---|---|---|
| `id` | uuid PK | |
| `tenant_id` | uuid NOT NULL FK tenants | |
| `supply_id` | uuid NOT NULL FK supplies ON DELETE CASCADE | `UNIQUE(supply_id)` — un almacén único (F1) |
| `stock_base` | integer NOT NULL DEFAULT 0 | **SIN CHECK: puede ser negativo** (R4) |
| `min_quantity` | integer NULL | en **unidad base**; `CHECK (min_quantity >= 0)`; null = sin mínimo |
| `max_quantity` | integer NULL | en **unidad base**; `CHECK (max_quantity >= 0)`; null = sin máximo |
| `updated_at` | timestamptz NOT NULL DEFAULT now() | |

- Fila **lazy** (como `supply_branch_stock`): se crea al primer `purchase` o al primer
  guardado de mín/máx. Supply sin fila = stock 0, sin mín/máx (lo interpreta el listado con
  LEFT JOIN sobre `supplies`).
- `max ≥ min` cuando ambos definidos: validación **en la capa service** (no CHECK, para
  permitir setear uno a la vez sin conocer el otro; ver §4.1).

### 2.3 `warehouse_movements` — ledger del almacén (fuente de verdad, F7/F12)

| Columna | Tipo | Notas |
|---|---|---|
| `id` | uuid PK | |
| `tenant_id` | uuid NOT NULL FK tenants | |
| `supply_id` | uuid NOT NULL FK supplies ON DELETE CASCADE | |
| `type` | text NOT NULL | `CHECK (type IN ('purchase','dispatch','waste'))` |
| `quantity_base` | integer NOT NULL | **FIRMADO**: `purchase (+)`, `dispatch (−)`, `waste (−)`; `CHECK (quantity_base <> 0)` |
| `branch_id` | uuid NULL FK branches | **solo `dispatch`**: sucursal destino (**requerido** en service). `purchase` y `waste` → NULL (la merma con sucursal NO vive aquí; ver §3.3) |
| `supplier_id` | uuid NULL FK suppliers ON DELETE SET NULL | solo `purchase` |
| `packages` | integer NULL | solo `purchase`: nº de presentaciones compradas; `CHECK (packages > 0)` |
| `unit_cost_cents` | integer NULL | solo `purchase`: **precio por presentación** en centavos (F5); `CHECK (unit_cost_cents >= 0)` |
| `reason` | text NULL | requerido en `waste` (service) |
| `created_by` | uuid NULL FK users | usuario que registró |
| `created_at` | timestamptz NOT NULL DEFAULT now() | fecha del movimiento (ver §4.4) |

Invariante: `warehouse_stock.stock_base == SUM(warehouse_movements.quantity_base)` por
`supply_id`. Índices: `(tenant_id, supply_id, created_at)`, `(tenant_id, type, created_at)`,
`(branch_id)`, `(supplier_id)`.

**Precio por presentación (F5):** se reusa el patrón `supplies.package*`. `quantity_base` de
una compra = `packages * supplies.package_content`. `unit_cost_cents` es el análogo de
`package_cost_cents` **para esa compra puntual** (no muta el costo del catálogo). Total de la
compra (derivado) = `packages * unit_cost_cents`.

### 2.4 Extensión de `supply_movements` (pata de sucursal — ADR-008 D2/D3)

Dos tipos nuevos en `supply_movements`, ambos escritos por el módulo de almacén:
- **`transfer`** (+): entrada a la sucursal por una **salida** de almacén (§3.2).
- **`waste`** (−): **merma de producto que ya estaba en la sucursal** (§3.3 caso b). Vive en
  el ledger de la sucursal porque los bienes están físicamente ahí, no en el almacén.

```sql
-- Tipos nuevos: 'transfer' (entrada por salida de almacén, +) y 'waste' (merma en
-- sucursal de producto ya despachado, −).
ALTER TABLE supply_movements DROP CONSTRAINT supply_movements_type_check;
ALTER TABLE supply_movements ADD CONSTRAINT supply_movements_type_check
    CHECK (type IN ('purchase','adjustment','sale','transfer','waste'));
-- Trazabilidad: liga la pata de sucursal (transfer) con su salida de almacén origen.
-- (En 'waste' de sucursal queda NULL: la merma no se ata a un dispatch puntual.)
ALTER TABLE supply_movements
    ADD COLUMN IF NOT EXISTS warehouse_movement_id uuid NULL
    REFERENCES warehouse_movements(id) ON DELETE SET NULL;
```

Orden en la migración: crear `suppliers` → `warehouse_stock` → `warehouse_movements` →
recién entonces alterar `supply_movements` (la FK apunta a `warehouse_movements`). `down`:
inverso (drop columna FK, restaurar CHECK sin `transfer`, drop tablas).

> **Nota migración:** el `down` que restaura el CHECK sin `transfer`/`waste` fallará si ya
> existen filas de esos tipos. Es el comportamiento correcto (no perder datos);
> documentarlo en el comentario del `.down.sql` como en 0018.

---

## 3. Flujos transaccionales

### 3.1 Compra (`purchase`) — F5, F7

Una transacción:
1. `INSERT warehouse_movements (type='purchase', quantity_base = +packages*package_content, supplier_id, packages, unit_cost_cents, created_by)`.
2. `UPSERT warehouse_stock` `stock_base += quantity_base` (`ON CONFLICT (supply_id) DO UPDATE`).

### 3.2 Salida (`dispatch`) — F8, F9, F10 · núcleo de R1/R2 (ADR-008)

Una transacción (los 4 efectos, orden determinista por `supply_id` en los upserts,
anti-deadlock, como `deductSupplies`):
1. `INSERT warehouse_movements (type='dispatch', quantity_base = −qty, branch_id=destino, created_by)` → `mwId`.
2. `UPSERT warehouse_stock` `stock_base −= qty`.
3. `INSERT supply_movements (type='transfer', quantity_base = +qty, branch_id=destino, warehouse_movement_id=mwId, created_by)`.
4. `UPSERT supply_branch_stock` `stock_base += qty`.

Esto materializa **R1**: la salida **suma** a `supply_branch_stock`; la venta
(`deductSupplies`) **resta**. Mismo dato, dos flujos complementarios, ambos con su registro
en `supply_movements` (ledger auditable). **F10** se cumple gratis: el `transfer` aparece con
su fecha en `GET /supplies/{id}/movements` (historial de la sucursal, endpoint existente).

### 3.3 Merma (`waste`) — F11, F12 · R3

La merma es **un concepto de negocio** que **bifurca según dónde están físicamente los
bienes** (determinado por `branch_id`). Descuenta el ledger del dominio correcto —nunca el
otro— para no crear déficit fantasma ni dejar la sucursal inflada.

**Caso (a) — merma en el almacén (`branchId` ausente):** el producto se echó a perder
estando en el almacén, nunca salió. Una transacción:
1. `INSERT warehouse_movements (type='waste', quantity_base = −qty, branch_id=NULL, reason, created_by)`.
2. `UPSERT warehouse_stock` `stock_base −= qty`.

**Caso (b) — merma en la sucursal (`branchId` presente):** el producto **ya había salido**
del almacén hacia esa sucursal (vía un `dispatch` que ya descontó `warehouse_stock` y sumó
`supply_branch_stock`) y se desechó **ahí**. Los bienes ya no están en el almacén, así que la
merma **NO toca `warehouse_stock`**; descuenta el **stock de la sucursal**. Una transacción:
1. `INSERT supply_movements (type='waste', quantity_base = −qty, branch_id=sucursal, reason, created_by)`.
2. `UPSERT supply_branch_stock` `stock_base −= qty` (mismo mecanismo que `insertMovement`).

Así cada dominio descuenta solo lo que realmente perdió, y la merma de sucursal queda además
**auditable desde la propia sucursal** (`GET /supplies/{id}/movements`), igual que `transfer`
y `sale` — coherente con que `supply_movements` sea la única fuente de verdad del stock de la
sucursal. `warehouse_movements` sigue siendo la única fuente de verdad del stock del almacén,
con su invariante intacta (nunca lleva `waste` con sucursal).

---

## 4. Decisiones R1-R4 (con justificación)

### 4.1 R4 — Stock negativo: **permitido, no bloqueante**

`warehouse_stock.stock_base` **sin CHECK de no-negatividad**, igual que
`supply_branch_stock` (0015) y consistente con `deductSupplies`. Una salida/merma sin
existencias suficientes **se registra igual** y deja el stock negativo. No hay razón de
negocio fuerte para cambiar el patrón ya establecido en producción; introducir bloqueo aquí
crearía una inconsistencia de comportamiento entre los dos ledgers.

- El frontend **puede** advertir (soft) "Sin existencias suficientes en el almacén" antes de
  confirmar (el handoff §6 lo soporta), pero el backend **no** rechaza. Es aviso, no gate.
- Validación de mín/máx (`max ≥ min`) sí es dura en service (400) cuando ambos vienen
  definidos en el mismo PATCH o al combinarse con el valor persistido.

### 4.2 R2 — Modelo del almacén: **entidad nueva + `transfer` en sucursal** (ADR-008)

Almacén = entidad nueva (`warehouse_stock`/`warehouse_movements`), no sucursal virtual. La
pata de sucursal de la salida es un `supply_movement` tipo **`transfer`** (positivo), ligado
por FK `warehouse_movement_id`. No hay doble contabilidad: son **dominios de stock
distintos** y la salida es un traspaso que cruza la frontera (sale del almacén, entra a la
sucursal). Detalle y alternativas en **ADR-008**.

### 4.3 R3 — Mermas: **concepto único que bifurca por ubicación; coexiste con `adjustment`**

La merma es **un solo concepto de negocio** (F11) cuyo **almacenamiento bifurca según dónde
están los bienes** (§3.3), porque hay dos dominios de stock (ADR-008) y la merma debe
descontar el correcto:

- **Merma en almacén** (sin sucursal) → `warehouse_movements.waste` (−), descuenta
  `warehouse_stock`.
- **Merma en sucursal** (con sucursal) → `supply_movements.waste` (−), descuenta
  `supply_branch_stock` de esa sucursal. **No** toca el almacén (los bienes ya salieron vía
  `dispatch`; descontar el almacén otra vez sería doble contabilidad invertida).

Esto **coexiste** con `adjustment` (no lo reemplaza):

- `adjustment` = corrección manual **arbitraria** del stock de una sucursal, por cualquier
  motivo, firmada (+/−). Sigue existiendo tal cual; mecanismo y datos históricos **no se
  tocan**. Se distingue de `waste` por el `type` (no se mezclan en la historia de mermas ni
  en el historial de la sucursal, donde se etiquetan "Ajuste" vs "Merma").
- `waste` = merma del flujo compra→almacén→sucursal, con motivo obligatorio.

Se usa un tipo `waste` propio en `supply_movements` (en vez de reusar `adjustment`) para que
la merma de sucursal sea **inequívocamente distinguible** de un ajuste manual —tanto en el
historial de mermas (§5.5, que hace UNION de ambos ledgers) como en la propia historia de la
sucursal— sin heurísticas frágiles sobre el `reason`.

**Sin migración de datos:** los `adjustment` históricos quedan intactos; no se reinterpretan.
Los conceptos no son equivalentes (uno es corrección arbitraria; el otro, merma con motivo).

### 4.4 Fecha del movimiento (forms con campo Fecha, F5/F8/F11)

Los tres forms capturan **Fecha** (`type=date`, default hoy). Se mapea a
`warehouse_movements.created_at`:
- Si la fecha == hoy (o ausente) → `now()` (preserva orden del ledger).
- Si es una fecha pasada (backdating) → esa fecha a mediodía UTC.

Un único timestamp (sin columna extra), consistente con cómo `supply_movements` ordena y
muestra el historial. El `transfer` espejo de una salida hereda el mismo `created_at` que su
`dispatch`.

---

## 5. Contratos de API (REST/JSON, estilo `internal/supplies/handler.go`)

Todas bajo `/warehouse`, **todas `RequireSuperAdmin`**. Envelopes como supplies:
`{ "items": [...] }` para listas, `{ "<recurso>": {...} }` para singulares. Errores con
`writeError(code, mensaje)` (mismos códigos: `validation_error`, `not_found`,
`invalid_branch`, `invalid_supply`, `conflict`/`name_taken`, `internal`).

### 5.1 Stock del almacén + mín/máx (F1-F4)

| Método | Ruta | Descripción |
|---|---|---|
| `GET` | `/warehouse/stock` | Todos los `supplies` (LEFT JOIN `warehouse_stock`) con `stockBase`, `minQuantity`, `maxQuantity`, datos de presentación. |
| `PATCH` | `/warehouse/stock/{supplyId}` | Upsert de mín/máx del insumo. Body: `{ "minQuantity": int|null, "maxQuantity": int|null }`. Valida `max ≥ min`. |
| `GET` | `/warehouse/to-buy` | Insumos con `minQuantity` definido **y** `stockBase ≤ minQuantity`. Incluye `missing = min − stock`. |

Item de `GET /warehouse/stock`:
```json
{
  "supplyId": "…", "name": "Leche entera", "baseUnit": "ml",
  "packageName": "Bote 900 ml", "packageContent": 900,
  "stockBase": 4200, "minQuantity": 2000, "maxQuantity": 9000,
  "status": "below_min" | "ok" | "no_min"
}
```
`status` es derivado en el backend (regla del badge, handoff §2.1) para no duplicar lógica en
el front: `below_min` si `min` definido y `stock ≤ min`; `no_min` si `min` null; si no `ok`.

### 5.2 Proveedores (F6)

| Método | Ruta | Body / Notas |
|---|---|---|
| `GET` | `/warehouse/suppliers` | Lista (`?status=active` opcional; default todos). |
| `POST` | `/warehouse/suppliers` | `{ name*, address?, email?, phone? }` → 201 `{ "supplier": {…} }`. `name` duplicado → 409. |
| `PATCH` | `/warehouse/suppliers/{id}` | Parcial `{ name?, address?, email?, phone?, status? }`. Baja = `status:"inactive"`. |

### 5.3 Compras (F5, F7)

| Método | Ruta | Body / Notas |
|---|---|---|
| `POST` | `/warehouse/purchases` | `{ supplyId*, supplierId*, packages* (>0), unitCostCents* (≥0), date? }` → 201 `{ "movement": {…}, "stockBase": int }`. |
| `GET` | `/warehouse/purchases` | Historial (`?from&to` RFC3339 opcional, LIMIT 100 DESC). Incluye `supplierName`, `packages`, `unitCostCents`, `totalCents` (derivado), `createdByName`, `createdAt`. |

`supplyId`/`supplierId` deben ser del tenant (400 `invalid_supply` / `invalid_supplier`).

### 5.4 Salidas (F8-F10)

| Método | Ruta | Body / Notas |
|---|---|---|
| `POST` | `/warehouse/dispatches` | `{ supplyId*, branchId*, quantityBase* (>0), date? }` → 201 `{ "movement": {…}, "warehouseStockBase": int, "branchStockBase": int }`. Ejecuta el flujo §3.2. |
| `GET` | `/warehouse/dispatches` | Historial con `branchName`, `quantityBase` (negativo), `createdByName`, `createdAt`. |

`branchId` requerido y del tenant (400 `invalid_branch`).

### 5.5 Mermas (F11, F12)

| Método | Ruta | Body / Notas |
|---|---|---|
| `POST` | `/warehouse/waste` | `{ supplyId*, quantityBase* (>0), reason*, branchId? (null=almacén central), date? }` → 201 `{ "movement": {…}, "stockBase": int }`. **Bifurca según `branchId`** (§3.3): sin sucursal escribe `warehouse_movements.waste` y `stockBase` = stock del almacén resultante; con sucursal escribe `supply_movements.waste` y `stockBase` = stock de **esa sucursal** resultante. |
| `GET` | `/warehouse/waste` | Historial de mermas = **UNION** de `warehouse_movements` (type=waste, branch NULL) y `supply_movements` (type=waste, branch set), ordenado por `created_at` DESC (LIMIT 100). Columnas: `branchName` (null → "—"), `reason`, `quantityBase` (negativo), `createdByName`, `createdAt`. |

`reason` requerido (400 `validation_error`). `branchId` si viene, del tenant (400
`invalid_branch`). La respuesta de `POST` indica en `movement.origin` (o análogo) desde qué
ledger se escribió, para que el front sepa que `stockBase` es de almacén o de sucursal; el
front puede ignorarlo y simplemente refrescar el historial.

### 5.6 Impacto en contratos existentes (sucursal)

- `GET /supplies/{id}/movements` (existente) ahora **también** devuelve movimientos
  `type:"transfer"` (positivos, entrada por salida de almacén → cumple **F10**) y
  `type:"waste"` (negativos, merma de producto ya despachado a esa sucursal → §3.3 caso b).
  El `Movement` model gana implícitamente ambos tipos en el string `type` (sin cambio de
  struct). **Frontend:** el historial de sucursal debe etiquetar `transfer` (ej. "Entrada de
  almacén") y `waste` (ej. "Merma") en el mapa `movementTypeLabel`.
- `POST /supplies/{id}/movements` **no cambia**: sigue rechazando todo type ≠
  `purchase|adjustment` (ADR-008 D4).

---

## 6. Contrato con frontend (resumen para frontend-engineer)

- Rutas `/warehouse`, `/warehouse/to-buy`, `/warehouse/purchases`, `/warehouse/dispatches`,
  `/warehouse/waste`, `/warehouse/suppliers` (+ `new`/`edit`), todas gated a
  `me.isSuperAdmin` (handoff §1.2).
- El badge de estado viene **derivado** del backend (`status` en §5.1), pero el front puede
  recomputar "Faltan" (`min − stock`) y el banner de reposición desde los mismos datos.
- `transfer` (entrada, +) y `waste` (merma de sucursal, −) en el historial de `supplies/[id]`
  (§5.6) — etiquetar ambos en `movementTypeLabel`.
- Precio: `unitCostCents` en centavos (patrón `packageCostCents`); mostrar/enviar en pesos ×100.
- F14 (limpieza de `/supplies/[id]/edit`) es puramente frontend; no requiere cambio de
  contrato backend (los endpoints `/supplies/{id}/movements` siguen existiendo).

## 7. Contrato con devops (resumen)

- Sin infra nueva. Solo la migración `0019_warehouse` (up/down) por el runner de migraciones
  existente. No hay variables de entorno nuevas ni dependencias externas.
- La migración altera un CHECK de una tabla en producción (`supply_movements`): es rápida
  (metadata, sin reescritura de filas) pero conviene aplicarla en la ventana de deploy
  estándar. `down` documentado (§2.4 nota).

---

## 8. Plan de pruebas (alto nivel)

**Migración**
- `0019 up` sobre una DB con datos de supplies/movements vivos: no rompe filas existentes;
  CHECK admite `transfer`; `down` revierte (falla esperada si hay filas `transfer`).

**Unitarias / service**
- Compra: `quantity_base == packages*package_content`; total derivado; upsert lazy crea la
  fila de `warehouse_stock`; supplier/supply ajenos → 400.
- Salida: 4 efectos en una tx; rollback total si cualquiera falla; `warehouse_stock −= qty`
  **y** `supply_branch_stock += qty`; se crea el `transfer` con `warehouse_movement_id`.
- Merma **sin** `branchId`: escribe `warehouse_movements.waste`, descuenta `warehouse_stock`,
  **no** toca ninguna sucursal. `reason` obligatorio.
- Merma **con** `branchId`: escribe `supply_movements.waste`, descuenta `supply_branch_stock`
  de esa sucursal, **no** toca `warehouse_stock` ni `warehouse_movements`.
- **Escenario 100/10 galletas (regresión del bug corregido):** dispatch 100 a sucursal S
  (almacén −100, S +100) → merma con branchId=S de 10 → verificar: `warehouse_stock` queda en
  −100 (solo por el dispatch, **sin** el déficit fantasma de −10) y `supply_branch_stock` de S
  queda en 90. Ambos lados cierran.
- Mín/máx: `max < min` → 400; setear uno solo; badge `status` correcto en los 3 casos.
- R4: salida/merma con stock insuficiente → **200** y stock negativo (no bloquea), en el
  ledger del dominio que corresponda.

**Integración / API**
- Gating: usuario no super_admin → 401/403 en todas las rutas de `/warehouse`.
- `to-buy` devuelve exactamente los insumos con min definido y `stock ≤ min`.
- Tras un dispatch, `GET /supplies/{id}/movements` de la sucursal destino incluye el
  `transfer` con su fecha (verifica F9+F10 end-to-end).
- Aislamiento por tenant en las 3 tablas nuevas.

**Concurrencia**
- Dos dispatches concurrentes del mismo insumo/sucursal: orden de bloqueo determinista
  (upsert por `supply_id`), sin deadlock, invariantes de ambos cachés consistentes con sus
  ledgers.

---

## 9. Notas abiertas (producto, no técnico)

- **Merma con sucursal (F11-b) — RESUELTA.** El producto ya despachado y desechado en la
  sucursal descuenta el **stock de esa sucursal** (`supply_branch_stock` vía
  `supply_movements.waste`), **no** el almacén (§3.3 caso b, §4.3). Coincide con el ejemplo de
  Silvin (100 galletas despachadas, 10 mermadas → sucursal queda en 90, almacén sin déficit
  fantasma). No quedan notas abiertas que bloqueen task-breakdown.
</content>
