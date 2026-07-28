# Task breakdown — Insights (insights)
_Autor: project-manager · Fecha: 2026-07-27 · Módulo: M9 · Gate: tareas atómicas_
_Fuentes: prd.md (aprobado, Silvin 2026-07-27) · design/handoff.md · tech-spec.md (contratos definidos) · ADR-009_
_Consumido por: backend-engineer · frontend-engineer · qa-engineer_

> Convenciones: cada tarea es ejecutable por **un** especialista sin bloquearse esperando otra
> tarea del mismo tamaño. Tallas: **S** ≤ medio día · **M** ~1 día · **L** ~2 días.
> Backend en `/Users/silvio/Projects/faro` (Go, `internal/insights`); frontend en
> `/Users/silvio/Projects/faro-ui` (Next.js). Módulo **solo lectura**, **gated a super_admin +
> branch_admin** (tech-spec §2), y **CERO LLM en toda ruta** (requisito no funcional duro: ninguna
> tarea introduce llamadas a un modelo).
> **Sin migración de base de datos en v1.** El índice `0020_sales_customer_idx` queda
> **explícitamente diferido** (tech-spec §3, ADR-009 D3) — no es tarea de v1. La verificación de
> `0010` en prod está **confirmada aplicada** — no es tarea pendiente.
> **División de pruebas:** cada tarea backend incluye en su **Done** un test unitario de camino
> feliz (shape correcto del struct). La **matriz completa de casos borde** de tech-spec §8 la
> poseen las tareas de QA (Q1–Q4), para no duplicar dueño del mismo test.
> ⚠️ **Insight 6 se implementa contra `loyalty_redemptions`, NO contra `sales.loyalty_reward`**
> (esa columna no existe — la borró `0010`; ver ADR-009 / tech-spec R1). El PRD/handoff quedan
> corregidos por el ADR: seguir la spec, no la letra del PRD.

---

## Orden de alto nivel (camino crítico)

```
Backend:
B1 skeleton + scope/gating + wiring ─┬─► B2 recurrence ────┐
                                     ├─► B3 top-products ───┤
                                     ├─► B4 ticket-segments ┤
                                     ├─► B5 second-visit ───┼─► (endpoints listos)
                                     ├─► B6 basket-affinity ┤
                                     └─► B7 loyalty-effect ─┘   (B2..B7 independientes entre sí)

Frontend (arranca en paralelo al backend; integra al cerrar cada endpoint):
F1 extraer ReportFilters (presets) ─┐
F2 lib/insights.ts (contra §4/§5) ──┤
F3 sidebar Insights (independiente) │
F5 primitivas de insight ───────────┤
                                     ├─► F4 página /insights (scaffold + carga aislada) ─┐
                                     │                                                    ├─► F6..F11 tarjetas
                                     └────────────────────────────────────────────────────┘  (cada una: F2+F4+F5+su endpoint)

QA:
B1 ─► Q1 gating       B2,B3,B4 ─► Q2 borde 1-3      B5,B6,B7 ─► Q3 borde 4-6      (todo backend) ─► Q4 transversal
```

Reglas de dependencia real:
- El **skeleton + scope/gating (B1)** va antes que cualquier insight (todos usan `resolveScope`,
  `branchClause` y el `Routes()` montado).
- Los **6 insights (B2–B7) son independientes entre sí**: comparten solo el scope de B1. Se pueden
  paralelizar entre backend-engineers.
- En frontend, **cada tarjeta (F6–F11)** necesita el cliente **F2**, el scaffold de página **F4** y
  las primitivas **F5**, más su endpoint backend correspondiente cerrado.
- **F1 (extracción de `ReportFilters`)** toca Reportes: es prerrequisito de **F4** y **no debe
  alterar** el comportamiento actual de Reportes (regresión).

---

## Backend (Go · `internal/insights`)

### B1 — Skeleton del módulo + scope/gating + wiring · **M** · dep: ninguna
Crear el paquete `internal/insights/` con la tríada (mismo patrón que `internal/reports`):
`model.go`, `store.go`, `service.go`, `handler.go`, `response.go` (copia de `reports/response.go`).
- `model.go`: `Scope{TenantID, From, To, TZ, Branch}`, `BranchFilter` (copiado de `reports/model.go`)
  y los structs de respuesta **stub** de los 6 insights (tags JSON según §4/§5, centavos).
- `store.go`: copiar `branchClause(alias, nextIdx, f)` de `reports/store.go` (mismo contrato de
  placeholders posicionales); **solo `SELECT`**, sin `Exec`/`Begin`.
- `handler.go`: `resolveScope(w, r)` **réplica exacta** de `reports.resolveReportScope` (tech-spec
  §2): 401 sin sesión · **403 para cashier/barista** · super_admin con `branchId` libre
  (`none|null` = bucket sin sucursal) · branch_admin **forzado** a `ActiveBranchFromContext`
  (400 `branch_required` si no hay activa, ignora `?branchId`). `parseRange` + `tz` como reports.
  `Routes(requireSession)` registra las **6 rutas GET** (§4) apuntando a handlers stub (200 vacío).
- **Wiring:** `insights.NewService(pool)` en `cmd/api/main.go`; `server.New(...)` gana
  `insightsSvc *insights.Service`; `r.Mount("/insights", insightsSvc.Routes(authSvc.RequireSession))`
  en `internal/server/server.go`.
- **Done:** compila y arranca (smoke); las 6 rutas responden 200 con stub; `resolveScope` aplica el
  gating (cashier/barista→403, sin sesión→401, branch_admin sin sucursal→400) verificado con un test
  de handler de camino feliz; `branchClause` copiado y usable; **ninguna** ruta escribe ni llama a
  un LLM.

### B2 — Insight 1: `/insights/recurrence` (F1, F2, F3, F3b) · **M** · dep: B1
Implementar store+service+handler de recurrencia (tech-spec §5.1), tres queries:
(a) tasa de recurrencia solo sobre identificados (`with_ge1`, `with_ge2`); (b) bucket anónimas
(`anon_sales`, `total_sales`); (c) cohorte de nuevos 30/60/90 con **primera venta histórica** en el
rango y retorno que **mira más allá de `to`** (F3). Aritmética de %/tasa en Go con guarda de
división por cero (`with_ge1==0` → `empty:true`). Respuesta `RecurrenceInsight` (§5.1).
- **Done:** endpoint devuelve `{withGe1, withGe2, ratePct, anonSales, totalSales, anonSharePct,
  cohort:{n,ret30,ret60,ret90}, empty}`; tasa calculada **solo** sobre identificados; bucket anónimas
  se devuelve aunque no haya identificados; sin división por cero en rango vacío; test unit de camino
  feliz; scope+branch aplicados.

### B3 — Insight 2: `/insights/top-products` (F4–F8) · **L** · dep: B1 · **T4**
Implementar los tres rankings + excluidos de margen (tech-spec §5.2). Base `product_sales`
(ingresos `SUM(line_total_cents)`, volumen `SUM(quantity)`; anónimas cuentan a nivel línea).
`recipe_cost` = `Σ(quantity_base × package_cost_cents / package_content)` en `numeric` sobre
`product_supplies ⋈ supplies`, con **`bool_or(package_cost_cents IS NULL) AS has_null_cost`**.
Ranking de margen excluye `has_null_cost=true` y productos sin receta (por el `JOIN`). Lista
`excludedFromMargin` con `reason: 'no_recipe' | 'null_cost'` (incluye producto borrado
`product_id NULL`).
- **⚠️ Nota crítica (F7):** `SUM()` ignora NULL en SQL; **no** confiar en la suma para detectar costo
  faltante — es el `bool_or` el que excluye. Nunca asumir costo 0 ni margen 100%.
- **Done:** endpoint devuelve `{byRevenue, byVolume, byMargin, excludedFromMargin}`; producto con un
  insumo `package_cost_cents NULL` → fuera de margen y en excluidos con `reason:"null_cost"`;
  producto sin receta → excluido con `reason:"no_recipe"`, presente en ingresos/volumen; producto
  borrado → cuenta en ingresos/volumen por snapshot `si.name`, fuera de margen; test unit de camino
  feliz; scope+branch aplicados.

### B4 — Insight 3: `/insights/ticket-segments` (F9, F10) · **M** · dep: B1
Implementar ticket promedio + conteo por segmento (tech-spec §5.3). Partición por **el mismo umbral
que Insight 1** (recurrente = `n≥2`, nuevo = `n=1`, anónimas = `customer_id NULL`), exhaustiva y sin
solapamiento (R2 resuelto, §5.3). `SUM(total_cents)` y `COUNT` por segmento; ticket = `spend/sales`
en Go con guarda (`sales==0` → "—"). Respuesta `TicketSegmentsInsight`.
- **Done:** endpoint devuelve `{new:{avgCents,sales}, recurring:{...}, anonymous:{...}}`; partición
  suma exactamente las ventas identificadas + anónimas aparte; usa el **mismo criterio de Insight 1**
  (sin dobles definiciones); segmento sin ventas → `avg="—", sales:0` sin romper; test unit de camino
  feliz; scope+branch aplicados.

### B5 — Insight 4: `/insights/second-visit` (F11–F13b) · **M** · dep: B1 · **T3**
Implementar visita-N=2 (tech-spec §5.4). `ROW_NUMBER() OVER (PARTITION BY customer_id ORDER BY
created_at, id)` sobre **historial completo** de identificados; `period_sales` = visitas cuyo evento
cae en `[from,to)`; fuerza = razón de sobre-representación (`share en visita#2 / share global`);
soporte = `cnt2` por producto. Umbral producto `$4` (**default 3**). Muestra insuficiente:
`n2_total < UMBRAL_2A_VISITA` (**default 5**) → `{insufficient:true, sampleSize:n2_total}` sin lista.
Anónimas excluidas por construcción (`customer_id NOT NULL`). Umbrales como parámetros con default
documentado.
- **Done:** endpoint devuelve `{insufficient, sampleSize, items:[{name, overRep, support, of}]}`; la
  "visita #2" es la 2ª del historial aunque la 1ª sea previa al rango; `n2_total<5`→insufficient sin
  lista; producto con `cnt2<3` no aparece; orden por `overRep` desc; anónimas nunca aparecen; test
  unit de camino feliz; scope+branch aplicados.

### B6 — Insight 5: `/insights/basket-affinity` (F14–F16) · **M** · dep: B1 · **T3**
Implementar afinidad de pares (tech-spec §5.5). `sale_products` = productos **distintos** por venta
del período (anónimas cuentan; `product_id NOT NULL`, borrados excluidos del emparejamiento).
`pairs` self-join con `a.product_id < b.product_id` (sin auto-par ni duplicado) +
`HAVING COUNT(*) >= $4` (**soporte mínimo default 3**). `lift = (support × N)/(freq_A × freq_B)`.
Orden por `support` desc, `lift` desc, `LIMIT 10`. Sin pares → `{insufficient:true}`. Umbral
parametrizable con default documentado.
- **Done:** endpoint devuelve `{insufficient, items:[{a, b, support, lift}]}`; par ≥ umbral aparece
  con `support`/`lift` correctos; par < umbral no aparece; sin auto-par ni `(A,B)/(B,A)` duplicado;
  productos borrados no forman pares; sin pares suficientes → `insufficient:true`; test unit de camino
  feliz; scope+branch aplicados.

### B7 — Insight 6: `/insights/loyalty-effect` (F17–F19) · **M** · dep: B1 · **T5 / ADR-009**
Implementar comparación canjeó vs. no canjeó (tech-spec §5.6). **Fuente = `loyalty_redemptions`**
(⚠️ **NO** `sales.loyalty_reward`, que no existe): `EXISTS(SELECT 1 FROM loyalty_redemptions r WHERE
r.sale_id = s.id)` por venta, agregado a nivel cliente con `bool_or(redeemed_sale)`. Solo
identificados (F19; anónimas excluidas por construcción). Por segmento: `customers`, `sales_total`,
`spend_total`; en Go: ticket promedio, ventas/cliente, gasto/cliente (guardas de división). Segmento
sin filas → `customers:0` conservando el otro. La atribución de sucursal se hereda vía `sales`
(`loyalty_redemptions` no tiene `branch_id`).
- **Done:** endpoint devuelve `{redeemed:{...}, notRedeemed:{...}}` con ticket/ventas-por-cliente/
  gasto-por-cliente derivados; segmento "canjeó" definido por `loyalty_redemptions`; sin canjes →
  `redeemed.customers:0`, columna "no canjeó" intacta; anónimas excluidas; **el código NO referencia
  `sales.loyalty_reward`**; test unit de camino feliz; scope+branch aplicados.

---

## Frontend (Next.js · `faro-ui`)

### F1 — Extraer `ReportFilters` compartido con presets configurables (handoff §6.1) · **M** · dep: ninguna
Extraer el bloque de filtros de `app/(app)/reports/page.tsx` a un componente reutilizable
`components/ReportFilters.tsx` (pills de rango + `Input type=date` Desde/Hasta + "Aplicar" + `Select`
de sucursal para super_admin), con **presets configurables** por prop. Semántica `[from,to)`+`tz`+
`branchId` idéntica. Reportes debe seguir funcionando **sin cambios de comportamiento** (mismos
presets Hoy/Ayer/Personalizado); Insights usará presets 30/90/Personalizado (default 30) en F4.
Validación `Desde ≤ Hasta` conservada.
- **Done:** `ReportFilters` acepta presets por prop; **Reportes migrado al componente sin regresión**
  (mismos presets, misma validación, mismo auto-refresh donde ya lo tenía); documentado en
  `foundations/design-system.md` §M9.1; sin cambios de contrato de API.

### F2 — `lib/insights.ts` (cliente API + tipos) · **S** · dep: B2–B7 (contratos §4/§5)
Cliente sobre `lib/api.ts` para los 6 GET (`recurrence`, `top-products`, `ticket-segments`,
`second-visit`, `basket-affinity`, `loyalty-effect`) con `?from&to&tz&branchId` (mismo contrato que
`/reports/sales`) + tipos TS espejo de las respuestas (§5, incl. banderas `empty`/`insufficient`/
`excludedFromMargin`/`customers:0`). Se puede empezar contra el contrato y verificar al cerrar cada
endpoint.
- **Done:** funciones tipadas por endpoint; manejo de `ApiError`; centavos in/out; banderas de estado
  reflejadas en los tipos; verificado contra endpoints reales.

### F3 — Sidebar: entrada "Insights" (handoff §1) · **S** · dep: ninguna
En `components/Sidebar.tsx` agregar **Insights** → ruta única `/insights`, **separada de Reportes**:
super_admin en sección propia "Insights" (agrupada); branch_admin como ítem plano junto a Reportes;
cashier/barista **no la ven**. Icono `Lightbulb` (lucide) — **evitar `Sparkles`** (el PRD descarta
connotación de IA). Estado activo = pastilla lime.
- **Done:** la entrada aparece con el gating correcto por rol; apunta a `/insights`; ítem activo
  resaltado; icono sin connotación de IA; separada de Reportes.

### F4 — Página `/insights`: scaffold + filtro + carga aislada por tarjeta (handoff §1.2, §2, §4) · **M** · dep: F1, F2
Crear `app/(app)/insights/page.tsx`: header (`h1` + sub-línea muted), `ReportFilters` (F1) con
presets **30 (default)/90/Personalizado**, **sin auto-refresh ni RefreshRing**. Gating de página
réplica de Reportes (super_admin filtro libre; branch_admin acotado, sin select; cashier/barista →
`Card` "No tienes acceso a los insights."). Contenedor de las 6 tarjetas en orden PRD (1→6) con
**carga aislada por tarjeta** (D3): cada tarjeta resuelve loading/vacío/insuficiente/error por su
cuenta; una que falla no tumba la página. Al cambiar cualquier filtro se recalculan las 6.
- **Done:** página monta con filtro compartido y presets de Insights; gating por rol correcto; las 6
  tarjetas se cargan de forma aislada (una en error no rompe el resto); recálculo al cambiar
  preset/sucursal/Aplicar; sin llamadas a LLM.

### F5 — Primitivas de insight al design-system (handoff §6.2–6.6) · **M** · dep: ninguna
Crear/formalizar las primitivas reutilizables: **Tarjeta de insight** (`Card` titular→detalle→estados
por tarjeta), **Stat/KPI block** (label muted + número grande `tabular-nums` + sub-línea de
respaldo), **Ranking list con barra** (`rank·label·valor` + barra proporcional, generaliza el
desglose por categoría de Reportes), **Comparación por segmentos** (2–3 columnas + fila de contraste
▲/▼/×), **Estado "muestra insuficiente"** (variante de vacío con copy propio, distinta de "vacío").
`tabular-nums`, dinero vía `toPesos`, `aria-label` en barras, áreas táctiles ≥44px.
- **Done:** las 5 primitivas renderizan sus variantes; distinción visible vacío vs. muestra
  insuficiente; documentadas en `foundations/design-system.md` §M9; sin lógica de negocio (solo
  presentación).

### F6 — Tarjeta Insight 1: Recurrencia (handoff §3.1) · **M** · dep: F2, F4, F5, B2
Titular llano "≈X de cada 10 clientes vuelven" + línea `Z% · N de M` (**siempre** num/den).
Cohorte 30/60/90 como 3 mini-stats con respaldo `k/N nuevos` + nota "la ventana puede mirar más allá
del rango". Bucket anónimas como línea de contexto muted (conteo + % del total), **separada** de la
tasa. Estados: loading / vacío ("Aún no hay clientes identificados…") **conservando** bucket anónimas
/ error danger.
- **Done:** muestra tasa con num/den/%, cohorte 30/60/90 con respaldo, bucket anónimas visible y
  separado; estado vacío conserva anónimas; nunca mezcla anónimas en la tasa; consume B2.

### F7 — Tarjeta Insight 2: Producto estrella (handoff §3.2) · **M** · dep: F2, F4, F5, B3
Tres rankings lado a lado (`grid sm:grid-cols-3`, apilan en angosto): Por ingresos ($) · Por volumen
(u) · Por margen ($), cada uno etiquetado, top 5, con barra proporcional al máximo de su columna.
`Alert variant="warning"` bajo el ranking de margen con **conteo + lista** de `excludedFromMargin`
("sin costo capturado", nunca costo 0). Estados por ranking: vacío ("Sin ventas en el rango."); margen
vacío con ventas → "Aún no hay productos con costo de receta completo." + aviso; loading/error.
- **Done:** los 3 rankings etiquetados con barras; alert de excluidos con lista; margen puede estar
  vacío con ingresos/volumen presentes; estados por ranking; consume B3.

### F8 — Tarjeta Insight 3: Ticket por segmento (handoff §3.3) · **S** · dep: F2, F4, F5, B4
Tres columnas de stat (Nuevo · Recurrente · Anónimas), cada una con `$ promedio` grande + "N ventas"
de respaldo. Nota muted: misma definición que Insight 1 (sin dobles criterios). Anónimas como tercer
segmento explícito. Contraste opcional ("el recurrente gasta ~p% más"). Segmento sin ventas →
"— · 0 ventas". Loading/error.
- **Done:** tres segmentos con promedio + conteo; anónimas explícitas nunca sumadas a otros; segmento
  vacío no rompe; consume B4.

### F9 — Tarjeta Insight 4: 2ª visita (handoff §3.4) · **S** · dep: F2, F4, F5, B5
Lista de productos sobre-representados en visita #2 ordenada por desproporción; fila `producto ·
fuerza (p.ej. "1.9× vs. promedio") · respaldo ("en k de N segundas visitas")`. **Sin selector de N.**
Nota al pie muted: "No incluye ventas anónimas…". Estado **muestra insuficiente** con copy propio
("Muestra insuficiente: solo N clientes tienen una 2ª visita…"), distinto de vacío. Loading/error.
- **Done:** lista ordenada por sobre-representación con respaldo por producto; nota de exclusión de
  anónimas como info muted (no error); estado muestra insuficiente diferenciado; consume B5.

### F10 — Tarjeta Insight 5: Afinidad de canasta (handoff §3.5) · **S** · dep: F2, F4, F5, B6
Lista top de pares ordenada por fuerza; fila `A + B · fuerza (etiqueta + medida, p.ej. "Alta (2.4×)")
· respaldo (nº ventas juntas)`. Enunciado "Solo se muestran pares con respaldo suficiente." Sin nota
de anónimas (cuentan normal). Estado vacío/insuficiente: "Aún no hay pares de productos con
suficiente respaldo…". Loading/error.
- **Done:** lista de pares con fuerza + respaldo; pares bajo umbral no aparecen; estado
  vacío/insuficiente; consume B6.

### F11 — Tarjeta Insight 6: Efectividad de lealtad (handoff §3.6) · **M** · dep: F2, F4, F5, B7
Comparación dos columnas ("Canjeó lealtad" vs. "No canjeó") con filas Ticket promedio · Ventas por
cliente · Gasto por cliente + columna de contraste (▲/▼/×). Respaldo "Basado en a clientes que
canjearon · b que no." Nota al pie muted: "No incluye ventas anónimas…". Segmento vacío (sin canjes)
→ "Sin canjes de lealtad en este rango." en la columna "Canjeó", **conservando** "No canjeó".
Loading/error. (El front solo consume `redeemed`/`notRedeemed` de B7; la fuente `loyalty_redemptions`
es transparente para la UI.)
- **Done:** comparación de 2 columnas con contraste; segmento sin canjes conserva la otra columna;
  nota de exclusión de anónimas como info muted; consume B7.

---

## QA (qa-engineer · Go test con DB de prueba, estilo `reports_test.go` / `supplies_test.go`)

Baja de tech-spec §8 a tareas concretas. Poseen la **matriz completa de casos borde** (los backend
traen solo el test de camino feliz en su Done).

### Q1 — Gating & scope (§8 Gating) · **S** · dep: B1
- cashier y barista → **403** en las 6 rutas; sin sesión → **401**.
- super_admin sin `branchId` → todas; `branchId=<uuid>` acota; `branchId=none` → sin sucursal.
- branch_admin sin sucursal activa → **400 `branch_required`**; con activa → forzado a esa sucursal,
  **ignora `?branchId`**.
- **Done:** todos los casos pasan en CI sobre las 6 rutas.

### Q2 — Casos borde Insights 1–3 (§8) · **M** · dep: B2, B3, B4
- **I1:** `X de Y (Z%)` solo sobre identificados; anónimas fuera de la tasa; cohorte cliente que
  vuelve al día 45 → cuenta ret60/ret90 no ret30; **retorno posterior a `to` sí cuenta** (F3);
  rango sin identificados → `empty:true` sin div-por-cero, bucket anónimas presente.
- **I2:** rankings ingresos/volumen/margen correctos y etiquetados; insumo `package_cost_cents NULL`
  → fuera de margen + `reason:"null_cost"` (verifica que `SUM` **no** lo tomó como 0); sin receta →
  fuera + `reason:"no_recipe"` (presente en ingresos/volumen); borrado → cuenta por snapshot, fuera
  de margen; margen respeta rango y sucursal.
- **I3:** partición exhaustiva (`new + recurring = identificadas`, anónimas aparte); 1 venta→nuevo,
  ≥2→recurrente (mismo umbral que I1); segmento sin ventas → `avg="—", sales:0`.
- **Done:** todos los casos pasan en CI.

### Q3 — Casos borde Insights 4–6 + regresión de esquema (§8) · **M** · dep: B5, B6, B7
- **I4:** numeración global correcta (2ª visita del historial aunque la 1ª sea previa al rango);
  `n2_total<5` → `insufficient:true` sin lista; `cnt2<3` no se lista; `over_rep` desc; anónimas nunca
  aparecen.
- **I5:** par ≥ umbral con `support`/`lift` correctos; par < umbral ausente; sin auto-par ni
  `(A,B)/(B,A)`; borrados no forman pares; sin pares → `insufficient:true`.
- **I6:** segmento "canjeó" definido por **`loyalty_redemptions`**; cliente con ≥1 redención en el
  rango → "canjeó" para todas sus ventas del rango; sin canjes → `redeemed.customers:0`, "no canjeó"
  intacta; anónimas excluidas.
- **⚠️ Regresión de esquema (§8):** test/asersión que **falle explícitamente si alguien reintroduce
  `sales.loyalty_reward`** como fuente de Insight 6 (documentar el porqué: ADR-009 R1).
- **Done:** todos los casos pasan en CI, incluida la regresión de esquema.

### Q4 — Transversal (§8) · **S** · dep: B2, B3, B4, B5, B6, B7
- **Determinismo:** misma request dos veces ⇒ mismo JSON (sin IA, sin aleatoriedad).
- **Aislamiento por tenant** en las 6 queries (venta de otro tenant nunca aparece).
- **200 en estados vacíos/insuficientes** (no 500) en las 6 rutas, para que la carga aislada por
  tarjeta funcione (handoff §4).
- **Done:** los tres bloques pasan en CI.

---

## DevOps
**Sin tareas de v1.** Tech-spec §10 lo confirma: sin infra nueva, sin variables de entorno, **sin
migración requerida**. El índice `0020_sales_customer_idx` queda **diferido** (solo si aparece la
señal de tech-spec §7 / ADR-009). La migración `0010_loyalty_promotions` está **confirmada aplicada
en Neon prod** — no requiere verificación adicional.

---

## Notas abiertas / a reconciliar (no bloquean el arranque, pero avisarlas)
- **N1 — Insight 6 corrige la letra del PRD/handoff (ADR-009 R1).** El PRD (F17) y el handoff (§3.6)
  dicen `sales.loyalty_reward IS NOT NULL`; esa columna **no existe** (la borró `0010`). Se implementa
  contra `loyalty_redemptions` (tech-spec §5.6). Recogido en **B7** y **Q3** — no repetir el error ya
  detectado.
- **N2 — Umbrales calibrables (P5, tech-spec §5.4/§5.5).** Soporte mínimo de afinidad (default 3),
  soporte por producto en visita-N (default 3) y corte de "muestra insuficiente" (default 5) son
  **parámetros con default documentado**, ajustables por QA sobre datos reales de Vanta **sin cambiar
  el contrato**. No bloquean el arranque.
- **N3 — Semántica de sucursal en cohortes/visitas (informativo, tech-spec R5).** Con branch_admin,
  "nuevo" y "2ª visita" se calculan dentro de su sucursal. Vanta es single-branch hoy → sin efecto
  práctico; documentado por si crece a multi-sucursal.
- **N4 — Señal de revisión de rendimiento (tech-spec §7, ADR-009 D1).** Cálculo en vivo sin cache en
  v1. Si p95 de `/insights/*` > 1.5 s sostenido, o `sales` del tenant > ~200 000 filas, o
  `basket-affinity` > 2 s: primera medida = aplicar el índice diferido `0020`. No se actúa antes.
