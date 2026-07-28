# Tech-spec — Insights (insights)

_Autor: tech-lead · Fecha: 2026-07-27 · Módulo: M9 · Estado: **contratos definidos** (gate)_
_Fuentes: prd.md (aprobado, Silvin 2026-07-27) · design/handoff.md · internal/reports (patrón) · internal/supplies (0015/0016) · migraciones 0004/0008/0009/0010/0011 · ADR-009_
_Consumido por: backend-engineer · frontend-engineer · devops-engineer_

> Esta spec define **el qué técnico** de Insights: gating, contratos de API, queries SQL
> exactas por insight (T1–T5), estructura del módulo y plan de pruebas. El **cómo detallado**
> de Go/SQL es del backend-engineer. La UI ya está resuelta en `design/handoff.md` (no se toca).
> **Requisito no funcional duro: CERO LLM.** Todo es agregación determinística SQL/Go.
> Decisión de fondo (cálculo en vivo + fuente real de canje de lealtad) en **ADR-009**.

---

## 0. Resumen de las 5 resoluciones pedidas (T1–T5)

| Ítem | Resolución | Riesgo |
|---|---|---|
| **T1 Gating** | Replica **exacta** de `reports.resolveReportScope`: super_admin (branchId libre) + branch_admin (forzado a sucursal activa); cashier/barista → 403. Mismo mount `RequireSession`. | Ninguno. Patrón ya en prod. |
| **T2 Volumen** | **Cálculo en vivo** (SQL por request, como `reports`), sin cache/precómputo. Es además lo **más barato** en Neon (CU-hora): un cron de precómputo *añadiría* compute base. Señal de revisión definida en §7. | Bajo. |
| **T3 Afinidad / fuerza** | Afinidad = **co-ocurrencia (soporte) + lift** como fuerza (no confidence, no Apriori). Visita-N = **razón de sobre-representación** (share en visita#2 / share global) + soporte. | Bajo. |
| **T4 Costo de receta** | Costo unitario = `Σ (quantity_base × package_cost_cents / package_content)` sobre `product_supplies ⋈ supplies`. **`bool_or(package_cost_cents IS NULL)`** excluye del margen (nunca asume 0). Sin receta = sin fila = excluido. | Bajo. Detalle SUM-ignora-NULL en §5.2. |
| **T5 Canje de lealtad** | ⚠️ **`sales.loyalty_reward` NO EXISTE** en el esquema real (la borró `0010_loyalty_promotions`, línea 92). El canje vive en **`loyalty_redemptions`**. Insight 6 se implementa contra esa tabla, **no** contra la columna del PRD. | **Medio — ver R1 en §9.** Corrige PRD/handoff. |

---

## 1. Arquitectura del módulo

Nuevo módulo backend `internal/insights/` (monolito modular, **mismo patrón que
`internal/reports`**), montado en **`/insights`**, con la tríada
`handler.go` / `service.go` / `store.go` / `model.go` (+ `response.go` copiado de reports).
Se cablea en `cmd/api/main.go` (`insights.NewService(pool)`) y `internal/server/server.go`
(`r.Mount("/insights", insightsSvc.Routes(authSvc.RequireSession))`).

```
[ faro-ui /insights ]  ──HTTP/JSON (cookie)──►  [ Go: internal/insights ]  ──►  Postgres
  (super_admin / branch_admin)                    SOLO LECTURA sobre tablas                (Neon)
                                                  existentes: sales, sale_items,
                                                  products, product_supplies, supplies,
                                                  loyalty_redemptions, customers, branches
```

- **Solo lectura, sin mutaciones.** El módulo no tiene `INSERT/UPDATE/DELETE`. No hay migración
  de datos ni tablas nuevas propias (ver §3; solo un índice **opcional** diferible).
- **Sin IA.** Ninguna ruta llama a un LLM ni servicio externo. Todo es SQL + aritmética Go.
- **Un endpoint por insight** (§4): la UI carga cada tarjeta por separado (handoff §4, "carga
  aislada por tarjeta"); un insight lento o que falla no tumba la página. Cada endpoint es
  independiente y comparte el mismo `scope` (tenant + `[from,to)` + `branchId` + `tz`).
- **Reusa el scope de reports:** `BranchFilter`, `branchClause`, parseo de `from/to/tz` y la
  autorización por rol se copian del patrón de `internal/reports` (§2), sin modificar reports.

---

## 2. T1 — Gating y scope (réplica exacta de reports)

Confirmado leyendo `internal/reports/handler.go` (`resolveReportScope`, líneas 26-70) y
`internal/server/server.go` (línea 77, `r.Mount("/reports", reportsSvc.Routes(authSvc.RequireSession))`).
El módulo replica ese patrón **al pie de la letra**:

```go
// insights/handler.go — idéntico en espíritu a reports.resolveReportScope
func (svc *Service) resolveScope(w http.ResponseWriter, r *http.Request) (scope Scope, ok bool) {
    u, okU := auth.UserFromContext(r.Context())
    if !okU { writeError(w, 401, "unauthorized", "Sesión requerida"); return }

    // Autorización por rol: SOLO super_admin y branch_admin. Igual que reports.
    if !u.IsSuperAdmin && u.Role != auth.RoleBranchAdmin {
        writeError(w, 403, "forbidden", "No autorizado para ver insights"); return
    }
    tenantID, okT := auth.ResolveTenant(w, r)
    if !okT { return }

    // Rango: el frontend siempre envía from/to (default "últimos 30 días", handoff §2).
    // Backend mantiene el mismo fallback defensivo que reports (hoy) si faltan.
    from, to := parseRange(r) // mismo parseTime RFC3339 que reports
    tz, _ := strconv.Atoi(r.URL.Query().Get("tz"))

    var branch BranchFilter
    if u.IsSuperAdmin {
        if b := r.URL.Query().Get("branchId"); b != "" {
            if b == "none" || b == "null" { branch.None = true } else { branch.ID = &b }
        }
    } else { // branch_admin: SIEMPRE forzado a su sucursal activa; ignora ?branchId
        active, _ := auth.ActiveBranchFromContext(r.Context())
        if active == nil { writeError(w, 400, "branch_required", "Selecciona una sucursal activa"); return }
        branch.ID = active
    }
    return Scope{TenantID: tenantID, From: from, To: to, TZ: tz, Branch: branch}, true
}
```

- **super_admin:** `?branchId=<uuid>` acota; `?branchId=none|null` = bucket "Sin sucursal"; sin
  param = todas. (Nota: para Insights 4 y 6, que operan sobre secuencias de cliente, el bucket
  `none` produce cohortes de ventas sin sucursal; es válido pero de poco interés — la UI de
  super_admin normalmente usará "Todas".)
- **branch_admin:** forzado a `ActiveBranchFromContext`; sin selector de sucursal (handoff §1.2).
- **cashier / barista:** 403 en **todas** las rutas de `/insights` (igual que reports hoy).
- **tz** (`Date.getTimezoneOffset()` en minutos) se usa igual que reports para cualquier corte
  horario; en Insights v1 ningún insight agrupa por hora local, así que `tz` se acepta por
  consistencia de contrato pero no altera resultados (los cortes son por `[from,to)` en UTC,
  idéntico a los resúmenes de reports). El rango `from/to` ya viene calculado por el frontend
  con la zona del cliente, como en reports.

`Scope` reemplaza el retorno multi-valor de reports por un struct (más limpio con 6 handlers);
`BranchFilter` y `branchClause(alias, nextIdx, f)` se copian tal cual de `reports/model.go` y
`reports/store.go` (mismo contrato de placeholders posicionales).

---

## 3. Modelo de datos — sin tablas nuevas; un índice opcional diferible

Insights es **solo lectura sobre tablas existentes**. Tablas y columnas usadas (todas
verificadas en migraciones):

| Tabla | Columnas usadas | Origen |
|---|---|---|
| `sales` | `id, tenant_id, customer_id, branch_id, total_cents, created_at` | 0004 + 0008 (`customer_id`) + 0011 (`branch_id`) |
| `sale_items` | `sale_id, product_id, name, quantity, line_total_cents` | 0004 |
| `products` | `id, name` | 0003 |
| `product_supplies` | `product_id, supply_id, quantity_base, tenant_id` | 0015 |
| `supplies` | `id, package_content, package_cost_cents` | 0015 + 0016 (`package_cost_cents`, nullable) |
| `loyalty_redemptions` | `sale_id, customer_id, tenant_id, created_at` | 0010 (⚠️ ver R1) |
| `customers` | (no requerida; nombres no se muestran en insights agregados) | 0008 |
| `branches` | `id, name` (solo si algún insight etiqueta sucursal; v1 no lo necesita) | 0011 |

**No hay migración de esquema requerida.** Índices existentes que ya cubren las queries:
- `sales_tenant_created_idx (tenant_id, created_at DESC)` — resúmenes por rango.
- `sale_items_sale_id_idx (sale_id)` — self-join de afinidad y agregación por venta.
- `loyalty_redemptions_tenant_customer_idx (tenant_id, customer_id, created_at)`.
- `product_supplies_product_id_idx`, `supplies` PK.

**Índice opcional (diferible, NO en v1 salvo que QA lo pida) — migración `0020_sales_customer_idx`:**
```sql
CREATE INDEX IF NOT EXISTS sales_tenant_customer_created_idx
    ON sales (tenant_id, customer_id, created_at);
```
Ayuda al `ROW_NUMBER() OVER (PARTITION BY customer_id ORDER BY created_at)` (Insight 4) y a la
determinación de "primera venta histórica" (Insight 1 cohorte) y a Insight 6. Es aditivo y
barato, pero con el volumen de Vanta el planner resuelve bien con el índice de tenant existente.
**Decisión: no se crea en v1;** se deja escrito para aplicarlo si la señal de §7 aparece. Un
índice extra en Neon cuesta escritura marginal en cada venta y espacio — innecesario hoy.

---

## 4. Contrato de API (REST/JSON, estilo `internal/reports`)

Seis rutas GET bajo `/insights`, **todas `RequireSession` + gating §2**. Query params comunes:
`?from=<RFC3339>&to=<RFC3339>&tz=<minutos>&branchId=<uuid|none>` (mismo contrato que
`/reports/sales`). Respuestas JSON directas (sin envelope `items` salvo listas internas).
Errores con `writeError(code, mensaje)` (mismos códigos que reports: `unauthorized`,
`forbidden`, `branch_required`, `internal`).

| Método | Ruta | Insight | Devuelve |
|---|---|---|---|
| GET | `/insights/recurrence` | 1 | tasa recurrencia + cohorte 30/60/90 + bucket anónimas |
| GET | `/insights/top-products` | 2 | 3 rankings (ingresos/volumen/margen) + excluidos de margen |
| GET | `/insights/ticket-segments` | 3 | ticket promedio + conteo por nuevo/recurrente/anónimas |
| GET | `/insights/second-visit` | 4 | productos sobre-representados en visita #2 + soporte |
| GET | `/insights/basket-affinity` | 5 | top de pares co-comprados + lift + soporte |
| GET | `/insights/loyalty-effect` | 6 | comparación canjeó vs. no canjeó |

**Estados de datos escasos**: cada endpoint devuelve un cuerpo válido (no error) cuando no hay
datos suficientes, con banderas que la UI ya sabe leer (handoff §3/§4):
- Insight 1/3: si no hay clientes identificados, los buckets identificados van en 0 y el bucket
  anónimas se sigue devolviendo. `"empty": true` en la sección identificada.
- Insight 4/5: `"insufficient": true` + `"sampleSize"` cuando el soporte total no alcanza el
  umbral (§5.4/§5.5). La UI muestra "muestra insuficiente" (distinto de "vacío").
- Insight 6: si un segmento no tiene clientes, ese segmento va con `customers: 0` y la UI
  conserva el otro.

Los umbrales concretos (soporte mínimo de afinidad, corte de "muestra insuficiente") son
**parámetros del backend con default documentado** (§5.4/§5.5), calibrables sin cambiar contrato.

---

## 5. Queries SQL exactas por insight

> Convenciones: `$1=tenant_id`, `$2=from`, `$3=to`; el filtro de sucursal se inyecta con
> `branchClause(alias, nextIdx, branch)` (copiado de reports) — se muestra como `[branch]` en el
> alias correspondiente. Dinero en centavos. El servicio calcula promedios/porcentajes en Go
> (nunca división en SQL que pueda dar `NaN`/div-by-zero; se traen sumas y conteos crudos).

### 5.1 Insight 1 — Recurrencia (F1, F2, F3, F3b)

**(a) Tasa de recurrencia** — solo clientes identificados en el rango:
```sql
WITH per_customer AS (
  SELECT customer_id, COUNT(*) AS n
  FROM sales
  WHERE tenant_id = $1 AND created_at >= $2 AND created_at < $3
    AND customer_id IS NOT NULL /*[branch]*/
  GROUP BY customer_id
)
SELECT COUNT(*) AS with_ge1,                       -- denominador (≥1 venta)
       COUNT(*) FILTER (WHERE n >= 2) AS with_ge2  -- numerador (recurrentes)
FROM per_customer;
```
Go: `rate = with_ge2 / with_ge1` (si `with_ge1 = 0` → estado vacío, sin división).

**(b) Bucket anónimas** (F3b) — conteo y % sobre total de ventas del período:
```sql
SELECT COUNT(*) FILTER (WHERE customer_id IS NULL) AS anon_sales,
       COUNT(*)                                    AS total_sales
FROM sales
WHERE tenant_id = $1 AND created_at >= $2 AND created_at < $3 /*[branch]*/;
```
Go: `anonShare = anon_sales / total_sales`.

**(c) Cohorte de nuevos 30/60/90** (F2, F3 — mira más allá de `to`):
La cohorte = clientes cuya **primera venta histórica** (todo el historial, no la ventana) cae
en `[from,to)`. El "¿volvió?" mira el historial real, **sin** tope en `to`.
```sql
WITH first_sale AS (
  SELECT customer_id, MIN(created_at) AS first_at
  FROM sales
  WHERE tenant_id = $1 AND customer_id IS NOT NULL /*[branch]*/
  GROUP BY customer_id
),
cohort AS (
  SELECT customer_id, first_at FROM first_sale
  WHERE first_at >= $2 AND first_at < $3
)
SELECT
  COUNT(*) AS cohort_n,
  COUNT(*) FILTER (WHERE EXISTS (
    SELECT 1 FROM sales s WHERE s.tenant_id = $1 AND s.customer_id = c.customer_id /*[branch]*/
      AND s.created_at >  c.first_at
      AND s.created_at <= c.first_at + INTERVAL '30 days')) AS ret30,
  COUNT(*) FILTER (WHERE EXISTS (
    SELECT 1 FROM sales s WHERE s.tenant_id = $1 AND s.customer_id = c.customer_id /*[branch]*/
      AND s.created_at >  c.first_at
      AND s.created_at <= c.first_at + INTERVAL '60 days')) AS ret60,
  COUNT(*) FILTER (WHERE EXISTS (
    SELECT 1 FROM sales s WHERE s.tenant_id = $1 AND s.customer_id = c.customer_id /*[branch]*/
      AND s.created_at >  c.first_at
      AND s.created_at <= c.first_at + INTERVAL '90 days')) AS ret90
FROM cohort c;
```
> **Semántica de sucursal (documentada):** el filtro `[branch]` se aplica **uniformemente** a
> `first_sale` y a la detección de retorno. Para un branch_admin, "nuevo" significa "primera
> venta *en su sucursal*" y "volvió" = "volvió *a su sucursal*". Para super_admin "Todas" no hay
> filtro (primera venta global). Esto es consistente y sin doble criterio; se anota porque en
> multi-sucursal cambia el significado (hoy Vanta es single-branch → sin efecto práctico).

**Respuesta** (`RecurrenceInsight`): `{ withGe1, withGe2, ratePct, anonSales, totalSales,
anonSharePct, cohort: { n, ret30, ret60, ret90 (conteos) }, empty }`. Porcentajes derivados en Go.

### 5.2 Insight 2 — Producto estrella (F4–F8) · **T4 costo de receta**

**Base de ventas por producto** (ingresos + volumen; anónimas cuentan, nivel línea):
```sql
WITH product_sales AS (
  SELECT si.product_id,
         COALESCE(p.name, si.name, 'Producto eliminado') AS name,
         SUM(si.line_total_cents) AS revenue_cents,
         SUM(si.quantity)         AS units
  FROM sale_items si
  JOIN sales s ON s.id = si.sale_id
  LEFT JOIN products p ON p.id = si.product_id
  WHERE s.tenant_id = $1 AND s.created_at >= $2 AND s.created_at < $3 /*[branch s.]*/
  GROUP BY si.product_id, COALESCE(p.name, si.name, 'Producto eliminado')
)
```
- **Ranking ingresos (F4):** `ORDER BY revenue_cents DESC LIMIT 5`.
- **Ranking volumen (F5):** `ORDER BY units DESC LIMIT 5`.

**Costo de receta por producto (T4, F6/F7):**
```sql
WITH recipe_cost AS (
  SELECT ps.product_id,
         bool_or(sp.package_cost_cents IS NULL) AS has_null_cost,
         SUM( (ps.quantity_base::numeric * sp.package_cost_cents) / sp.package_content )
             AS unit_cost_cents            -- costo de insumos por UNIDAD vendida del producto
  FROM product_supplies ps
  JOIN supplies sp ON sp.id = ps.supply_id
  WHERE ps.tenant_id = $1
  GROUP BY ps.product_id
)
```
**Detalle crítico (por qué `bool_or`):** `SUM()` en SQL **ignora los NULL**. Si un insumo tiene
`package_cost_cents = NULL`, su término desaparece de la suma y el `unit_cost_cents` quedaría
*subestimado* (como si ese insumo fuera gratis) — exactamente lo que F7 prohíbe. Por eso el
`bool_or(package_cost_cents IS NULL)` marca el producto para **excluirlo del ranking de margen**,
en vez de confiar en la suma. Costo por unidad = `Σ (quantity_base × package_cost_cents /
package_content)`; se calcula en `numeric` (evita truncar la división por presentación) y se
redondea al final.

**Ranking de margen (F6/F7/F8):**
```sql
SELECT ps.name,
       ps.revenue_cents,
       ps.revenue_cents - ROUND(rc.unit_cost_cents * ps.units)::bigint AS margin_cents
FROM product_sales ps
JOIN recipe_cost rc ON rc.product_id = ps.product_id
WHERE rc.has_null_cost = false          -- excluye productos con algún insumo sin costo
ORDER BY margin_cents DESC
LIMIT 5;
```
- Producto **sin receta** → no aparece en `recipe_cost` → el `JOIN` lo excluye del margen
  (presente en ingresos/volumen). ✔ criterio de aceptación.
- Producto con **algún** insumo `package_cost_cents NULL` → `has_null_cost = true` → excluido. ✔
- Producto con `product_id = NULL` (borrado) → no matchea `product_supplies` → excluido del
  margen (cuenta en ingresos/volumen por el snapshot `si.name`). ✔

**Lista de excluidos del margen (F7 — visibilidad "sin costo capturado"):**
```sql
SELECT ps.name,
       CASE WHEN rc.product_id IS NULL THEN 'no_recipe' ELSE 'null_cost' END AS reason
FROM product_sales ps
LEFT JOIN recipe_cost rc ON rc.product_id = ps.product_id
WHERE ps.product_id IS NULL              -- borrado (no puede tener receta)
   OR rc.product_id IS NULL              -- sin receta
   OR rc.has_null_cost = true            -- receta con insumo sin costo
ORDER BY ps.revenue_cents DESC;
```
**Respuesta** (`TopProductsInsight`): `{ byRevenue:[{name,revenueCents,units}],
byVolume:[...], byMargin:[{name,revenueCents,marginCents}],
excludedFromMargin:[{name,reason}] }`.

### 5.3 Insight 3 — Ticket promedio por segmento (F9, F10)

**Decisión de segmentación (documentada — resuelve la ambigüedad "misma definición que
Insight 1"):** la partición nuevo/recurrente de Insight 3 usa el **mismo umbral que la tasa de
Insight 1** (`≥2` = recurrente), aplicado a clientes identificados del período. Así:
- **recurrente** = cliente con `n ≥ 2` ventas en el período (= numerador de Insight 1),
- **nuevo** = cliente con `n = 1` venta en el período (= denominador − numerador),
- **anónimas** = `customer_id IS NULL` (tercer bucket, disjunto).

Esta partición es **exhaustiva y sin solapamiento** (cada venta identificada cae en exactamente
un bucket, sin exclusión silenciosa) y reusa literalmente el criterio de Insight 1 ("sin dobles
criterios", handoff §3.3). La definición canónica de "nuevo = primera venta histórica en rango"
del PRD se usa **solo** en la cohorte 30/60/90 de Insight 1 (que es un sub-análisis distinto),
no en esta segmentación de ticket. → **ver R2 en §9** (confirmación de Silvin recomendada, con
este default ya operativo).

```sql
WITH per_customer AS (
  SELECT customer_id, COUNT(*) AS n, SUM(total_cents) AS spend
  FROM sales
  WHERE tenant_id = $1 AND created_at >= $2 AND created_at < $3
    AND customer_id IS NOT NULL /*[branch]*/
  GROUP BY customer_id
),
anon AS (
  SELECT COUNT(*) AS sales_n, COALESCE(SUM(total_cents),0) AS spend
  FROM sales
  WHERE tenant_id = $1 AND created_at >= $2 AND created_at < $3
    AND customer_id IS NULL /*[branch]*/
)
SELECT
  COALESCE(SUM(spend) FILTER (WHERE n >= 2), 0) AS rec_spend,
  COALESCE(SUM(n)     FILTER (WHERE n >= 2), 0) AS rec_sales,
  COALESCE(SUM(spend) FILTER (WHERE n = 1),  0) AS new_spend,
  COALESCE(SUM(n)     FILTER (WHERE n = 1),  0) AS new_sales,
  (SELECT sales_n FROM anon) AS anon_sales,
  (SELECT spend   FROM anon) AS anon_spend
FROM per_customer;
```
Go: `ticket = spend / sales` por segmento (si `sales = 0` → "—", sin división). Cada segmento
lleva su conteo de ventas (F10). **Respuesta** (`TicketSegmentsInsight`):
`{ new:{avgCents,sales}, recurring:{avgCents,sales}, anonymous:{avgCents,sales} }`.

### 5.4 Insight 4 — Qué se pide en la 2ª visita (F11–F13b) · **T3 fuerza**

**Método (T3):** numeración de visitas por `ROW_NUMBER()` sobre el **historial completo** del
cliente; fuerza = **razón de sobre-representación** = `share del producto en visitas#2 /
share del producto en todas las visitas del período`. Soporte = nº de visitas#2 que incluyen el
producto (señal de tamaño de muestra, criterio de aceptación). Solo clientes identificados
(F13b: anónimas excluidas por imposibilidad estructural — no tienen `customer_id`).

```sql
WITH visit_seq AS (                       -- numeración GLOBAL (todo el historial)
  SELECT s.id AS sale_id, s.customer_id,
         ROW_NUMBER() OVER (PARTITION BY s.customer_id ORDER BY s.created_at, s.id) AS visit_no
  FROM sales s
  WHERE s.tenant_id = $1 AND s.customer_id IS NOT NULL /*[branch]*/
),
period_sales AS (                          -- visitas cuyo evento cae en el período
  SELECT vs.sale_id, vs.visit_no
  FROM visit_seq vs
  JOIN sales s ON s.id = vs.sale_id
  WHERE s.created_at >= $2 AND s.created_at < $3
),
n2 AS (SELECT sale_id FROM period_sales WHERE visit_no = 2),
tot AS (
  SELECT (SELECT COUNT(*) FROM n2)           AS n2_total,   -- muestra: nº de 2ª visitas
         (SELECT COUNT(*) FROM period_sales) AS all_total   -- base: todas las visitas del período
),
prod2 AS (                                  -- presencia del producto en 2ª visitas
  SELECT si.product_id, COALESCE(p.name, si.name) AS name,
         COUNT(DISTINCT si.sale_id) AS cnt2
  FROM sale_items si
  JOIN n2 ON n2.sale_id = si.sale_id
  LEFT JOIN products p ON p.id = si.product_id
  WHERE si.product_id IS NOT NULL
  GROUP BY si.product_id, COALESCE(p.name, si.name)
),
prodall AS (                                -- presencia del producto en TODAS las visitas
  SELECT si.product_id, COUNT(DISTINCT si.sale_id) AS cntall
  FROM sale_items si
  JOIN period_sales ps ON ps.sale_id = si.sale_id
  WHERE si.product_id IS NOT NULL
  GROUP BY si.product_id
)
SELECT prod2.name, prod2.cnt2, tot.n2_total,
       (prod2.cnt2::numeric / NULLIF(tot.n2_total,0))
       / NULLIF(prodall.cntall::numeric / NULLIF(tot.all_total,0), 0) AS over_rep
FROM prod2
JOIN prodall ON prodall.product_id = prod2.product_id
CROSS JOIN tot
WHERE prod2.cnt2 >= $4                       -- soporte mínimo por producto (default 3)
ORDER BY over_rep DESC NULLS LAST
LIMIT 10;
```
- **Muestra insuficiente:** el servicio trae `n2_total` (una fila basta; usar `LIMIT` o query
  aparte). Si `n2_total < UMBRAL_2A_VISITA` (**default 5** clientes con 2ª visita), devuelve
  `{ insufficient: true, sampleSize: n2_total }` sin lista (handoff §3.4). Umbral parametrizable.
- `$4` = soporte mínimo por producto (**default 3**), para no listar ruido.

**Respuesta** (`SecondVisitInsight`): `{ insufficient, sampleSize (n2_total),
items:[{name, overRep, support (cnt2), of (n2_total)}] }`.

### 5.5 Insight 5 — Afinidad de canasta (F14–F16) · **T3 método**

**Método (T3 — decisión y justificación):** **co-ocurrencia (soporte) como umbral + lift como
fuerza.** Se descarta confidence (es direccional A→B ≠ B→A, incómodo para una lista simétrica de
pares) y Apriori/FP-growth (multi-ítem, fuera de alcance del PRD). El lift es **barato** aquí:
requiere solo el conteo de co-ocurrencia por par y el conteo por producto individual (dos
agregaciones que ya calculamos), es **simétrico** (un valor por par) y **normalizado**
(comparable entre pares, se presenta como "2.4×", handoff §3.5). `lift = (soporte × N_ventas) /
(freq_A × freq_B)`.

**Complejidad (T2, la query "sospechosa"):** el self-join **no** es O(items²) global. Es
`Σ_ventas (k_i choose 2)` con `k_i` = productos DISTINTOS por venta. En una cafetería `k_i` es
un puñado (típicamente 1–6), así que el costo es ~lineal en el nº de ventas, no cuadrático en
ítems. El `HAVING support >= umbral` poda temprano. **No requiere precómputo** en v1. Anónimas
cuentan (nivel venta/línea). Productos con `product_id = NULL` (borrados) se **excluyen** del
emparejamiento (no se puede identificar el par de forma fiable).

```sql
WITH sale_products AS (                     -- productos DISTINTOS por venta del período
  SELECT DISTINCT si.sale_id, si.product_id, COALESCE(p.name, si.name) AS name
  FROM sale_items si
  JOIN sales s ON s.id = si.sale_id
  LEFT JOIN products p ON p.id = si.product_id
  WHERE s.tenant_id = $1 AND s.created_at >= $2 AND s.created_at < $3 /*[branch s.]*/
    AND si.product_id IS NOT NULL
),
pairs AS (
  SELECT a.product_id AS pa, a.name AS na, b.product_id AS pb, b.name AS nb,
         COUNT(*) AS support                -- nº de ventas que contienen AMBOS
  FROM sale_products a
  JOIN sale_products b
    ON a.sale_id = b.sale_id
   AND a.product_id < b.product_id          -- evita duplicar par y auto-par
  GROUP BY a.product_id, a.name, b.product_id, b.name
  HAVING COUNT(*) >= $4                       -- soporte mínimo (default 3)
),
freq AS (SELECT product_id, COUNT(*) AS cnt FROM sale_products GROUP BY product_id),
tot  AS (SELECT COUNT(DISTINCT sale_id) AS n FROM sale_products)
SELECT pairs.na, pairs.nb, pairs.support,
       (pairs.support::numeric * tot.n) / (fa.cnt * fb.cnt) AS lift
FROM pairs
JOIN freq fa ON fa.product_id = pairs.pa
JOIN freq fb ON fb.product_id = pairs.pb
CROSS JOIN tot
ORDER BY pairs.support DESC, lift DESC
LIMIT 10;
```
- `$4` = **soporte mínimo** (default **3** ventas conjuntas; P5 lo calibra QA sobre datos de
  Vanta). Pares por debajo no se muestran (F16).
- Ordena por soporte primero (respaldo real) y lift como desempate/fuerza mostrada.
- **Insuficiente:** si `pairs` sale vacío → `{ insufficient: true }` (handoff §3.5).

**Respuesta** (`BasketAffinityInsight`): `{ insufficient, items:[{a, b, support, lift}] }`.

### 5.6 Insight 6 — Efectividad de lealtad (F17–F19) · **T5**

> ⚠️ **Corrección de esquema (T5).** El PRD y el handoff dicen segmentar por
> `sales.loyalty_reward IS NOT NULL`. **Esa columna NO existe** en el esquema real: la agregó
> `0009_loyalty` y la **eliminó `0010_loyalty_promotions` (línea 92)** al migrar a lealtad v2.
> El canje real vive en la tabla **`loyalty_redemptions`** (0010): una fila por recompensa
> canjeada, con `sale_id`, `customer_id`, `tenant_id`, `created_at`. Insight 6 se implementa
> contra `loyalty_redemptions`, no contra la columna inexistente. Fundamento en **ADR-009**.

**Definición operativa:** un cliente "canjeó" si tiene **≥1 fila en `loyalty_redemptions`**
atribuida a alguna de sus ventas del período. Segmentación a nivel cliente (`bool_or`): un
cliente que canjeó al menos una vez cae en "canjeó" para todas sus ventas del período (F17:
"clientes que canjearon al menos una recompensa"). Solo identificados (F19).

```sql
WITH identified AS (
  SELECT s.id AS sale_id, s.customer_id, s.total_cents,
         EXISTS (SELECT 1 FROM loyalty_redemptions r WHERE r.sale_id = s.id) AS redeemed_sale
  FROM sales s
  WHERE s.tenant_id = $1 AND s.created_at >= $2 AND s.created_at < $3
    AND s.customer_id IS NOT NULL /*[branch s.]*/
),
cust AS (
  SELECT customer_id,
         bool_or(redeemed_sale) AS redeemed,
         COUNT(*)               AS sales_n,
         SUM(total_cents)       AS spend
  FROM identified
  GROUP BY customer_id
)
SELECT redeemed,
       COUNT(*)        AS customers,     -- clientes en el segmento
       SUM(sales_n)    AS sales_total,   -- ventas del segmento
       SUM(spend)      AS spend_total    -- gasto del segmento
FROM cust
GROUP BY redeemed;
```
Go, por cada segmento (`redeemed = true/false`):
- ticket promedio = `spend_total / sales_total`
- ventas por cliente (proxy recurrencia) = `sales_total / customers`
- gasto por cliente = `spend_total / customers`

Si un segmento no aparece (p. ej. sin canjes) → se devuelve con `customers: 0` y la UI conserva
la otra columna (criterio de aceptación). **Respuesta** (`LoyaltyEffectInsight`):
`{ redeemed:{customers,salesTotal,spendTotal,avgTicketCents,salesPerCustomer,spendPerCustomer},
notRedeemed:{...} }`.

`loyalty_redemptions` no tiene `branch_id`, pero la atribución de sucursal se hereda vía
`sales` (el `EXISTS` liga la redención a una venta ya filtrada por `[branch]`). Correcto.

---

## 6. Estructura del módulo backend `internal/insights`

```
internal/insights/
├── handler.go   // Routes(requireSession) + 6 handlers + resolveScope (§2)
├── service.go   // Service{store}; un método por insight (orquesta store + aritmética Go)
├── store.go     // 6 queries (§5); reusa branchClause; SOLO SELECT
├── model.go     // Scope, BranchFilter, structs de respuesta por insight
└── response.go  // writeJSON/writeError (copia de reports/response.go)
```
- **Sin mutaciones**: el store solo hace `pool.Query`/`QueryRow`. No hay `Exec`, `Begin`, ni
  transacciones (no hay nada que escribir).
- **Aritmética en Go**: promedios, porcentajes y ratios de presentación se calculan en el
  service a partir de sumas/conteos crudos, con guardas de división por cero (estado vacío).
- **Cableado**: `insights.NewService(pool)` en `main.go`; firma de `server.New(...)` gana
  `insightsSvc *insights.Service`; `r.Mount("/insights", insightsSvc.Routes(authSvc.RequireSession))`.

---

## 7. T2 — Cálculo en vivo, sin cache; señal de revisión

**Decisión (ADR-009): cálculo en vivo por request, sin precómputo ni cache en v1.** Razones:
1. **Costo Neon (CU-hora):** Neon cobra compute activo. Un job de precómputo/materialized view
   *añade* compute base recurrente; con el volumen de Vanta (una cafetería, single-tenant) las
   queries en vivo son de milisegundos y solo consumen compute cuando Silvin abre Insights. La
   opción más barata **es** la más simple aquí.
2. **Simplicidad sostenible:** sin capa de invalidación, sin drift, sin cron. Igual que `reports`.
3. **Determinismo:** mismos filtros ⇒ mismo resultado, sin estado intermedio.

**Señal que dispara revisar precómputo** (no implementar hasta que ocurra, revisar en ese
momento — no antes):
- **p95 de cualquier `/insights/*` > 1.5 s** sostenido en uso normal, **o**
- **filas en `sales` del tenant > ~200 000** (a decenas de ventas/día en Vanta, son años), **o**
- **`/insights/basket-affinity` > 2 s** específicamente (es el candidato natural por el
  self-join, aunque §5.5 argumenta que es ~lineal en ventas).

Primera medida ante la señal: aplicar el índice opcional `0020` (§3). Solo si persiste, evaluar
una vista materializada por insight refrescada fuera de hora (documentar en un ADR nuevo). El
PRD no impone arquitectura de cache y esta spec **no** la crea.

---

## 8. Plan de pruebas (alto nivel)

Mapeo de criterios de aceptación del PRD a casos de test (store con DB de prueba + handler para
gating), siguiendo el estilo de `reports_test.go` / `supplies_test.go`.

**Gating (T1)**
- cashier y barista → 403 en las 6 rutas; usuario sin sesión → 401.
- super_admin sin `branchId` → todas; con `branchId=<uuid>` acota; `branchId=none` → sin sucursal.
- branch_admin sin sucursal activa → 400 `branch_required`; con activa → forzado a esa sucursal,
  ignora `?branchId`.

**Insight 1 — Recurrencia**
- `X de Y (Z%)` correcto solo sobre identificados; ventas anónimas NO entran en la tasa.
- Cohorte: cliente cuya 1ª venta cae en el rango y vuelve al día 45 → cuenta en ret60/ret90 pero
  no en ret30; el retorno **posterior a `to`** se cuenta (F3).
- **Rango sin clientes identificados** → `empty:true`, sin división por cero; si hay anónimas,
  su bucket sigue presente.

**Insight 2 — Producto estrella (T4)**
- Rankings ingresos/volumen/margen correctos y etiquetados.
- Producto con un insumo `package_cost_cents = NULL` → **fuera** del margen y en
  `excludedFromMargin` con `reason:"null_cost"` (verifica que `SUM` no lo tomó como 0).
- Producto **sin receta** → fuera del margen, `reason:"no_recipe"`; presente en ingresos/volumen.
- Producto borrado (`product_id NULL`) → cuenta en ingresos/volumen por snapshot; fuera de margen.
- Margen respeta rango y sucursal.

**Insight 3 — Ticket por segmento**
- Partición exhaustiva: `sales(new) + sales(recurring) = ventas identificadas`; anónimas aparte.
- Cliente con 1 venta → nuevo; con ≥2 → recurrente (mismo umbral que Insight 1).
- Segmento sin ventas → `avg="—", sales:0` sin romper.

**Insight 4 — 2ª visita**
- Numeración global correcta: la "2ª visita" es la 2ª del historial aunque la 1ª sea previa al
  rango; se presenta si el evento de la 2ª visita cae en el rango.
- `n2_total < 5` → `insufficient:true` sin lista.
- Producto con `cnt2 < 3` no se lista; over_rep ordena descendente.
- Anónimas nunca aparecen.

**Insight 5 — Afinidad**
- Par que co-ocurre ≥ umbral aparece con `support` y `lift` correctos; par < umbral no aparece.
- Auto-par y par duplicado (A,B)/(B,A) no se generan (`product_id < product_id`).
- Sin pares suficientes → `insufficient:true`.
- Productos borrados no forman pares.

**Insight 6 — Lealtad (T5)**
- Segmento "canjeó" se define por **`loyalty_redemptions`** (no por columna inexistente);
  cliente con ≥1 redención en el rango → columna "canjeó" para todas sus ventas del rango.
- Sin canjes en el rango → `redeemed.customers:0`, columna "no canjeó" intacta.
- Anónimas excluidas.
- **Regresión de esquema:** un test/asersión que falle explícitamente si alguien reintroduce
  `sales.loyalty_reward` como fuente (documentar el porqué en el test).

**Transversal**
- Determinismo: misma request dos veces ⇒ mismo JSON (sin IA, sin aleatoriedad).
- Aislamiento por tenant en las 6 queries (una venta de otro tenant nunca aparece).
- Cada endpoint responde 200 con cuerpo válido en estados vacíos/insuficientes (no 500),
  para que la carga aislada por tarjeta funcione (handoff §4).

---

## 9. Riesgos e inconsistencias PRD/diseño ↔ esquema real

- **R1 — `sales.loyalty_reward` no existe (CRÍTICO, resuelto en spec).** T5 del PRD y §3.6 del
  handoff asumen `sales.loyalty_reward IS NOT NULL`. La migración `0010_loyalty_promotions`
  (línea 92) **eliminó** esa columna al pasar a lealtad v2. Insight 6 usa `loyalty_redemptions`
  (§5.6, ADR-009). **Acción:** implementar según esta spec; PRD/handoff quedan corregidos por
  ADR-009. No bloquea, pero **no** se debe implementar la letra del PRD.
- **R2 — Ambigüedad "nuevo" en Insight 3 (RESUELTA — Silvin, 2026-07-27).** Confirmado: usa la
  **partición por umbral de Insight 1** (nuevo = 1 venta en período, recurrente = ≥2 ventas),
  exhaustiva y sin solapamiento (§5.3). La definición canónica "primera venta histórica en rango"
  del PRD queda reservada exclusivamente a la cohorte de Insight 1 (F2/F3), no se usa aquí.
- **R3 — `loyalty_redemptions` podría estar vacía / ausente en prod (a verificar).** La memoria
  del proyecto marca "lealtad v2 pendiente de DB". Hay que **confirmar que `0010` está aplicada
  en Neon prod** antes de desplegar Insight 6. Si la tabla no existe, ese endpoint fallaría (500)
  — pero por la carga aislada por tarjeta no tumba el resto. Mitigación: verificar migración
  aplicada; si aún no lo está, Insight 6 mostrará estado vacío tras aplicarla. **Acción devops:**
  confirmar `0010` en prod.
- **R4 — Sin acceso a Neon prod para medir volumen (restricción real).** No se consultó el
  volumen real (sin credenciales). La arquitectura en vivo (§7) es robusta a volumen bajo y
  crece con señal explícita de revisión; no se asumió ningún número de negocio.
- **R5 — Semántica de sucursal en cohortes/visitas (informativo).** Con branch_admin, "nuevo"
  y "2ª visita" se calculan *dentro* de la sucursal (§5.1/§5.4). Hoy Vanta es single-branch, sin
  efecto; documentado por si crece a multi-sucursal.

---

## 10. Contratos para ingeniería (handoff)

- **→ backend-engineer:** módulo `internal/insights` (§6), 6 queries (§5), gating (§2),
  respuestas (§4/§5). **Ojo con R1**: Insight 6 va contra `loyalty_redemptions`, no
  `sales.loyalty_reward`. Sin migración de esquema (índice `0020` **diferido**).
- **→ frontend-engineer:** 6 endpoints `/insights/*` con `?from&to&tz&branchId` (mismo contrato
  que `/reports/sales`); banderas `empty` / `insufficient` / `excludedFromMargin` /
  `customers:0` para los estados que ya especifica el handoff §3/§4. Sin cambios de contrato
  respecto a la UI ya diseñada.
- **→ devops-engineer:** **sin infra nueva, sin variables de entorno, sin migración requerida**
  para v1. **Acción puntual:** confirmar que `0010_loyalty_promotions` está aplicada en Neon
  prod (R3) antes de exponer Insight 6. Índice opcional `0020` solo si aparece la señal de §7.
```
