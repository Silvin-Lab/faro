# Tech Spec — M12 convenio-discounts

_Autor: tech-lead · Fecha: 2026-10-07 · Rama: `feat/convenio-discounts` (faro + faro-ui)_
_Inputs: `brief.md` (alcance cerrado por el dueño), ADR-010 (decisiones irreversibles), ADR-005/007/009._

Esta spec define el **qué técnico** para backend, frontend y devops. Las decisiones duras
(columnas en `sales`, orden de cálculo, usuario de la venta) están en **ADR-010** y no se
re-litigan aquí; esta spec las implementa. Decisiones de producto cerradas por Silvin
(no re-litigar): lealtad + convenio se suman; botones solo con %; aplica a toda la compra;
todos los roles lo otorgan; catálogo dinámico con seed 10/15; venta ligada al usuario de la sesión.

---

## 1. Modelo de datos y migración 0031

### 1.1 Tabla del catálogo `agreement_discounts` (patrón `loyalty_promotions`, ADR-005)

```sql
CREATE TABLE IF NOT EXISTS agreement_discounts (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id  uuid NOT NULL REFERENCES tenants(id),
    percent    integer NOT NULL CHECK (percent BETWEEN 1 AND 100),
    status     text NOT NULL DEFAULT 'active', -- active | inactive (archivada)
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- % único por tenant SOLO entre los activos (permite archivar y recrear el mismo %).
CREATE UNIQUE INDEX IF NOT EXISTS agreement_discounts_tenant_percent_active_uq
    ON agreement_discounts (tenant_id, percent) WHERE status = 'active';

-- Lectura desde el POS (listar activos ordenados por %) y admin (?status).
CREATE INDEX IF NOT EXISTS agreement_discounts_tenant_status_idx
    ON agreement_discounts (tenant_id, status);
```

No hay tabla de productos (aplica a toda la compra) ni relación con convenios/clientes (fuera
de alcance). El orden en el POS es `ORDER BY percent ASC`.

### 1.2 Columnas nuevas en `sales` (ADR-010 §D1, §D4)

```sql
ALTER TABLE sales ADD COLUMN IF NOT EXISTS agreement_discount_id      uuid    REFERENCES agreement_discounts(id) ON DELETE SET NULL;
ALTER TABLE sales ADD COLUMN IF NOT EXISTS agreement_discount_percent integer CHECK (agreement_discount_percent BETWEEN 1 AND 100);
ALTER TABLE sales ADD COLUMN IF NOT EXISTS agreement_discount_cents   integer NOT NULL DEFAULT 0;
ALTER TABLE sales ADD COLUMN IF NOT EXISTS user_id                    uuid    REFERENCES users(id);

CREATE INDEX IF NOT EXISTS sales_user_id_idx ON sales (user_id);
```

- `discount_cents` **no cambia**: sigue siendo lealtad. `total_cents` sigue neto (ahora de
  ambos descuentos). Justificación completa en ADR-010 §D1 (rompe menos en reportes, insights
  y ticket; evita divergencia con `loyalty_redemptions`).
- `agreement_discount_percent` es snapshot (inmune a edición/archivado posterior del catálogo).
- `user_id` nullable: ventas históricas quedan NULL ("sin registro"). No hay backfill.
- No se agrega índice sobre `agreement_discount_*` en v1: el reporte filtra por
  `tenant_id + created_at` (ya indexado por `sales_tenant_created_idx`) y agrega en memoria/SQL
  por período acotado. Señal de revisión diferida si el volumen lo pide.

### 1.3 Seed 10% y 15% por tenant

```sql
INSERT INTO agreement_discounts (tenant_id, percent)
SELECT t.id, v.percent
  FROM tenants t CROSS JOIN (VALUES (10), (15)) AS v(percent)
ON CONFLICT DO NOTHING; -- idempotente si se re-corre
```

### 1.4 Migración `0031_agreement_discounts` (up/down)

- **up:** `BEGIN;` crea tabla + índices, `ALTER TABLE sales …`, seed, `COMMIT;`.
- **down:** `DROP INDEX … ; ALTER TABLE sales DROP COLUMN IF EXISTS …` (las 4 columnas),
  `DROP TABLE IF EXISTS agreement_discounts;` dentro de una transacción. Se pierde la atribución
  de convenio/usuario de ventas registradas mientras estuvo viva (igual criterio que 0011).
- Archivos: `migrations/0031_agreement_discounts.up.sql` y `.down.sql`.

---

## 2. Cálculo en el servidor (`internal/sales`, ADR-010 §D2/§D3)

Nuevo parámetro `agreementDiscountID *string` en `Service.Create` → `store.createSale`
(análogo a `promotionID`). El cliente **solo** manda el id; el servidor lee % y monto del
catálogo. Pipeline dentro de la transacción existente, tras calcular lealtad:

1. `subtotal`, `discountCents` (lealtad) — **sin cambios**.
2. Si `agreementDiscountID != nil && customerID != nil`:
   - `SELECT percent FROM agreement_discounts WHERE id=$1 AND tenant_id=$2 AND status='active'`.
     `pgx.ErrNoRows` / `dberr.IsInvalidText` ⇒ `ErrAgreementNotEligible`.
   - `remaining = subtotal - discountCents`
   - `agreementCents = (remaining*percent + 50) / 100` (half-up), `clamp [0, remaining]`.
   - Guardar snapshot: `agreementPercent = percent`, `agreementID`.
3. `total = subtotal - discountCents - agreementCents`.
4. Pago: **sin cambios** — `isExactPaymentMethod` ⇒ `amountPaid=total`; efectivo valida
   `amountPaidCents >= total` (`ErrInsufficientPayment`) y calcula cambio sobre el `total` neto.
5. `INSERT INTO sales (…, discount_cents, agreement_discount_id, agreement_discount_percent, agreement_discount_cents, user_id, branch_id)` — agregar las 4 columnas al INSERT y al RETURNING.
6. `deductSupplies`, incremento de visitas y `loyalty_redemptions`: **intactos** (el convenio
   no toca inventario ni lealtad).

### 2.1 Validaciones y errores

| Condición | Resultado |
|---|---|
| `agreementDiscountID` presente sin `customerID` | **RECHAZO** `ErrAgreementNotEligible` → HTTP **422** `agreement_discount_not_eligible` (gate del orquestador: no se ignora en silencio — evita que el POS cobre con descuento y la venta quede a precio completo). |
| descuento inexistente / de otro tenant / `inactive` | `ErrAgreementNotEligible` → HTTP **422** `agreement_discount_not_eligible` |
| resto (items, pago, cliente, producto, promo) | **sin cambios** |

`ErrAgreementNotEligible` nuevo en `store.go`; mapeo en `handler.go` junto a los existentes.
El servicio normaliza `agreementDiscountID` con `nilIfBlank`. A diferencia de `promotionID`
(que se anula sin cliente), si llega `agreementDiscountID != nil && customerID == nil` el
servicio devuelve `ErrAgreementNotEligible` (422) en vez de anularlo.

### 2.2 `user_id`

`handleCreate` obtiene `u, _ := auth.UserFromContext(r.Context())` y pasa `u.ID` a `Create`
(no viene del cuerpo). Se escribe en el INSERT. La respuesta incluye `soldByUserId` y
`soldByName` (se resuelve el nombre con un `JOIN users` como ya se hace con `branches`).

---

## 3. Contratos de API (todos retrocompatibles)

### 3.1 CRUD `/agreement-discounts` (nuevo módulo `internal/agreementdiscounts`, patrón loyalty)

Montado en `server.go`: `r.Mount("/agreement-discounts", svc.Routes(authSvc.RequireSession, authSvc.RequireSuperAdmin))`.

| Método | Ruta | Auth | Cuerpo / respuesta |
|---|---|---|---|
| GET | `/agreement-discounts?status=active\|inactive\|all` | **cualquier sesión** (el POS lee) | `{ "items": [AgreementDiscount] }` |
| GET | `/agreement-discounts/{id}` | cualquier sesión | `{ "discount": AgreementDiscount }` |
| POST | `/agreement-discounts` | **super_admin** | body `{ percent }` → `201 { "discount": … }` |
| PUT | `/agreement-discounts/{id}` | super_admin | body `{ percent }` → `200 { "discount": … }` |
| DELETE | `/agreement-discounts/{id}` | super_admin | archiva (`status='inactive'`) → `204` |

`AgreementDiscount = { id, percent, status, createdAt, updatedAt }`.

Validación (servicio): `percent` entero 1..100 ⇒ si no, `400 validation_error`. Colisión de %
activo (viola índice parcial) ⇒ `409 conflict` (`agreement_discount_duplicate`) — capturar el
error de unicidad de pgx y mapear. Archivar inexistente ⇒ `404 not_found`. Tenant se resuelve
con `auth.ResolveTenant` (super admin → negocio único), igual que loyalty.

### 3.2 `POST /sales` — campo nuevo

`createRequest` gana `AgreementDiscountID *string json:"agreementDiscountId"` (opcional). Los
clientes actuales que no lo envían ⇒ `null` ⇒ venta sin convenio. **Retrocompatible.**

### 3.3 Respuesta de venta (`Sale`) — campos nuevos, aditivos

Añadir al modelo `Sale` y a los SELECT de `createSale`, `get`, `listByTenant`:

```jsonc
{
  // … campos actuales sin cambios (discountCents sigue siendo lealtad) …
  "agreementDiscountId": "uuid|null",
  "agreementDiscountPercent": 10,        // o null
  "agreementDiscountCents": 1500,        // 0 si no hubo
  "soldByUserId": "uuid|null",
  "soldByName": "Nombre Apellido|null"   // null ⇒ "Sin registro"
}
```

Aplica a `GET /sales/{id}` (detalle + ticket) y `GET /sales` (historial del POS).

### 3.4 `GET /reports/sales/list` — historial de reportes

`SaleListItem` gana `agreementDiscountPercent (int|null)`, `agreementDiscountCents (int)`,
`soldByName (string|null)`. Añadir las columnas/JOIN al SELECT de `salesList`. Aditivo.

### 3.5 `GET /reports/sales` — bloque de resumen de convenio

`SalesReport` gana un bloque nuevo (no toca los existentes; `totalCents` sigue **neto**,
consistente con lealtad — recomendado y confirmado en el brief):

```jsonc
"agreementDiscounts": {
  "salesCount": 12,          // ventas con convenio en el período
  "totalCents": 18000,       // suma de agreement_discount_cents
  "byPercent": [             // desglose por %
    { "percent": 10, "count": 7, "totalCents": 9000 },
    { "percent": 15, "count": 5, "totalCents": 9000 }
  ]
}
```

SQL (respeta `branchClause` y rango `[from,to)` como el resto del reporte):

```sql
-- resumen
SELECT COUNT(*) FILTER (WHERE agreement_discount_cents > 0),
       COALESCE(SUM(agreement_discount_cents), 0)
  FROM sales WHERE tenant_id=$1 AND created_at>=$2 AND created_at<$3 <branchClause>;
-- por %
SELECT agreement_discount_percent, COUNT(*), COALESCE(SUM(agreement_discount_cents),0)
  FROM sales WHERE tenant_id=$1 AND created_at>=$2 AND created_at<$3
    AND agreement_discount_cents > 0 <branchClause>
  GROUP BY agreement_discount_percent ORDER BY agreement_discount_percent;
```

---

## 4. UI (faro-ui) — spec breve, sin diseñador

Seguir `design-system.md`. Reutilizar componentes de `/loyalty` (admin) y del panel de cobro del POS.

### 4.1 POS (`app/(app)/pos/page.tsx`, `lib/sales.ts`, nuevo `lib/agreementDiscounts.ts`)
- Fila de **botones de convenio** (uno por descuento activo, `ORDER BY percent`, etiqueta solo
  `"10%"`), junto al bloque de lealtad del panel de cobro.
- **Deshabilitados** mientras la venta no tenga cliente asociado (`!customer`); se habilitan al
  asociarlo (mismo gating que el botón de lealtad, que ya usa `disabled={!loyaltyStatus}`).
- Selección: un solo descuento por venta (toggle; tocar otro reemplaza). "Quitar" = volver a
  tocar el activo. Estado local `selectedAgreementDiscountId`.
- `createSale` pasa `agreementDiscountId` en `opts`. El **monto lo calcula el servidor**; el
  cliente puede previsualizar con la misma fórmula half-up (solo display, igual que ya hace con
  lealtad) pero muestra el total autoritativo que devuelve la venta.
- **Desglose de totales** en el panel: `Subtotal` → `Lealtad −$…` (si >0) → `Convenio (10%) −$…`
  (si >0) → `Total`. Mantener el bloque condicional actual, agregando la línea de convenio.

### 4.2 Admin — catálogo (`app/(app)/agreement-discounts/page.tsx` + entrada en `components/Sidebar.tsx`)
- Nueva entrada en el grupo **Clientes** del sidebar (junto a "Lealtad"), solo super_admin:
  `{ href: '/agreement-discounts', label: 'Convenios', icon: … }`.
- Lista de descuentos (%, estado), crear (input de %), archivar. Toggle activos/archivados.
  Mensaje claro al chocar con `agreement_discount_duplicate` (ya existe un activo con ese %).

### 4.3 Detalle de venta / ticket (POS e historial de reportes)
- Mostrar, si `agreementDiscountCents > 0`: línea "Convenio (X%) −$…".
- Mostrar "Cobró: {soldByName}" o "Cobró: Sin registro" si `soldByName == null`.

### 4.4 Reportes (`app/(app)/reports/page.tsx`)
- Nueva sección "Descuentos de convenio": `salesCount`, `totalCents`, y tabla `byPercent`
  (% · ventas · monto). Ubicar cerca del desglose por forma de pago.

---

## 5. Plan de pruebas — foco en REGRESIÓN de la venta

> `TEST_DATABASE_URL` **debe** apuntar a `faro_test`, nunca a `faro` (incidente 2026-08-06).
> Verificar antes de `go test ./...`.

### 5.1 Integración `internal/sales` (añadir a `sales` test suite)
1. **Venta normal** (sin cliente, sin descuentos): total = subtotal; `discount_cents=0`,
   `agreement_discount_cents=0`, `agreement_discount_id=NULL`, `user_id` = sesión. deductSupplies igual.
2. **Solo lealtad** (como hoy): convenio en 0; `total = subtotal − lealtad`; `loyalty_redemptions` escrito.
3. **Solo convenio**: `total = subtotal − round(subtotal*pct/100)`; lealtad en 0; cliente requerido.
4. **Lealtad + convenio**: `remaining = subtotal − lealtad`; `convenio = round(remaining*pct/100)`;
   `total = subtotal − lealtad − convenio`; verificar que cuadra al centavo (varios % y precios impares).
5. **Sin cliente + agreementDiscountId**: **422** `agreement_discount_not_eligible`, la venta
   **no** se crea (ajuste del orquestador que prevalece sobre el texto original del brief/ADR:
   rechazar en vez de ignorar en silencio, para no descuadrar la caja). Verificar `count(sales)==0`.
6. **Descuento inactivo / de otro tenant / inexistente**: `422 agreement_discount_not_eligible`,
   la venta **no** se crea (rollback).
7. **4 métodos de pago** (cash/card/transfer/didi) con convenio: no-cash cobran `total` exacto,
   `change=0`; cash valida `amountPaid >= total` y cambio correcto; insuficiente ⇒ `insufficient_payment`.
8. **deductSupplies intacto**: con convenio aplicado, los movimientos de insumos y el cache de
   stock son idénticos a la misma venta sin convenio (el descuento no toca inventario).
9. **user_id**: toda venta nueva lo guarda; `soldByName` resuelve; venta histórica (fila sin la
   columna poblada) ⇒ `null`.

### 5.2 Integración catálogo `internal/agreementdiscounts`
- CRUD feliz; `percent` fuera de 1..100 ⇒ 400; duplicado activo ⇒ 409; archivar y recrear mismo
  % ⇒ OK (índice parcial); lectura con sesión no-admin ⇒ OK; escritura no-admin ⇒ 403; aislamiento por tenant.

### 5.3 Reportes (`internal/reports`)
- **Antes/después:** `totalCents` y `salesCount` de `/reports/sales` **no cambian** para un set
  de ventas con convenio (sigue siendo neto). Nuevo bloque `agreementDiscounts` cuadra: suma de
  `byPercent.totalCents == totalCents`; `salesCount` cuenta solo ventas con convenio.
- `/reports/sales/list`: campos nuevos presentes; ventas sin convenio ⇒ 0/null.

### 5.4 Frontend
- Botones deshabilitados sin cliente; selección/quita; desglose de totales coincide con el
  `total` del servidor; render de "Sin registro"; sección de reportes.

---

## 6. Plan de despliegue

Orden y entorno (confirmados en el brief): este módulo sale **antes** que M10/M11; migraciones
0021–0030 reservadas por esas ramas **no existen** en prod todavía.

1. **Backend + frontend** mergeados y validados en **demo local en LAN** con Silvin (regla fija:
   implementar → demo local → confirmación → deploy).
2. **Migración a Neon a mano con psql**, sobre una base que llega hasta **0020**:
   ```
   psql "$NEON_PROD_URL" -f migrations/0031_agreement_discounts.up.sql
   ```
   La 0031 es autocontenida (no depende de 0021–0030). El seed es idempotente.
3. Verificar: `agreement_discounts` tiene 10% y 15% activos por tenant; `sales` tiene las 4
   columnas; ventas existentes con `agreement_discount_cents=0` y `user_id=NULL`.
4. Deploy backend (Fly) y frontend (Vercel). Smoke: crear venta con convenio, ver reporte, ver ticket.
5. Rollback: `0031_…down.sql` (pierde atribución de convenio/usuario de ventas del período; sin
   pérdida de datos preexistentes). Preferible rollback de app sin tocar esquema si es posible.

---

## 7. Preguntas abiertas para Silvin (solo bloqueantes)

Ninguna bloqueante. Decisiones tomadas por el tech lead que conviene **confirmar en la demo**
(no detienen la implementación):

1. **Etiqueta en el sidebar** del catálogo admin: propuesto "Convenios" en el grupo *Clientes*.
   Si prefieres otro texto/ubicación, se ajusta en 1 línea.
2. **Colisión de % activo**: se rechaza crear un segundo descuento activo con el mismo % (409).
   Asumido correcto por "percent único por tenant"; confirmar que no quieres duplicados activos.
