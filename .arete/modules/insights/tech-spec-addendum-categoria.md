# Tech-spec · Addendum — Vista por categoría (Insights 2 y 5)

_Autor: tech-lead · Fecha: 2026-07-28 · Módulo: M9 (insights) · Estado: **contratos definidos**_
_Extiende (no reemplaza): `tech-spec.md` §5.2 (Insight 2) y §5.5 (Insight 5)._
_Fuentes verificadas: `internal/insights/store.go` + `model.go` (implementación en prod) ·
`internal/reports/store.go` (precedente de agregación por categoría con manejo de "Sin categoría",
bloques "Por categoría" y "Por producto") · contexto de esquema confirmado por orquestador._
_Consumido por: backend-engineer · frontend-engineer._

> Extensión menor **ya aprobada por el dueño del negocio** (Silvin, en vivo tras ver M9 en prod).
> **No** requiere PRD nuevo, **no** requiere ADR nuevo (respeta ADR-009: cálculo en vivo, cero LLM,
> solo lectura) y **no** rediseña la UI (se agregan campos aditivos a 2 contratos existentes).
> Alcance: (2a) sumar 2 rankings por **categoría** (ingresos y volumen) al Insight 2, y (5a) sumar la
> vista de afinidad por **categoría** al Insight 5. **Margen por categoría queda FUERA** (ver §0).

---

## 0. Contexto de esquema y qué queda fuera

- `products.category_id` (uuid, **nullable**) → FK a `categories`. `sale_items` **no** guarda
  categoría; la categoría sale de unir `sale_items → products → categories` (`LEFT JOIN`, igual que
  `reports.salesReport`).
- Un `sale_item` puede tener categoría nula por **dos** caminos, ambos indistinguibles a nivel de
  agregado y ambos van al **mismo bucket "Sin categoría"** (decisión, consistente con `reports`):
  1. producto vivo con `category_id IS NULL`, o
  2. producto borrado (`si.product_id IS NULL` o sin fila en `products`) → sin categoría recuperable.
- **Margen por categoría: FUERA de v1.** Se descubrió en prod que la mayoría de los productos no
  tienen costo de receta capturado (ver `excludedFromMargin` con `reason:"no_recipe"`). Agregar
  margen por categoría hoy produciría un ranking calculado sobre una minoría no representativa de
  productos. Es una decisión de negocio ya tomada por Silvin: **no se implementa**. Cuando se capture
  costo de forma amplia, se reevalúa en un addendum posterior.

---

## 1. Insight 2 — Rankings por categoría (ingresos y volumen)

**Se agregan 2 rankings** a `/insights/top-products`, sin tocar los 3 por producto (§5.2 original).
Método idéntico a la CTE `product_sales`, pero agrupando por categoría con el patrón de `reports`.

### 1.1 Query (base + dos ordenamientos)

```sql
WITH category_sales AS (
  SELECT COALESCE(c.name, 'Sin categoría') AS name,
         SUM(si.line_total_cents)          AS revenue_cents,
         SUM(si.quantity)                  AS units
  FROM sale_items si
  JOIN sales s        ON s.id = si.sale_id
  LEFT JOIN products p   ON p.id = si.product_id
  LEFT JOIN categories c ON c.id = p.category_id
  WHERE s.tenant_id = $1 AND s.created_at >= $2 AND s.created_at < $3 /*[branch s.]*/
  GROUP BY COALESCE(c.name, 'Sin categoría')
)
-- Ranking por ingresos:
SELECT name, revenue_cents, units FROM category_sales ORDER BY revenue_cents DESC LIMIT 5;
-- Ranking por volumen:
SELECT name, revenue_cents, units FROM category_sales ORDER BY units       DESC LIMIT 5;
```

### 1.2 Tratamiento de bordes (decisiones explícitas)

- **Sin categoría = bucket propio, NO se descarta.** `COALESCE(c.name, 'Sin categoría')` — igual que
  `reports.salesReport`. Aparece en el ranking como cualquier otra fila.
- **Producto borrado** (`si.product_id IS NULL` o producto ya no existe) → `p`/`c` nulos → cae en el
  bucket **"Sin categoría"** (no se le puede atribuir su categoría original; no hay snapshot de
  categoría en `sale_items`). Se decide contar sus ingresos/volumen ahí en vez de perderlos.
  Consecuencia: "Sin categoría" mezcla productos vivos sin categoría + productos borrados. Es la
  misma semántica que ya usa el reporte de ventas, así que es coherente con lo que Silvin ya ve.
- **Anónimas cuentan** (agregación a nivel línea; no interviene `customer_id`), igual que los
  rankings por producto.
- `LIMIT 5` por paridad con los rankings de producto. Como un negocio suele tener pocas categorías,
  es aceptable subirlo o quitarlo sin cambiar contrato (es una lista); default documentado = 5.

### 1.3 Nota de implementación (backend)

Reusar el mismo patrón de `store.topProducts`: `branchClause("s.", 4, sc.Branch)`, `args :=
[tenant, from, to (+branch)]`, y `fmt.Sprintf` del `%s` de la cláusula de sucursal en el `WHERE` de
`category_sales`. Dos `pool.Query` (ingresos y volumen) que comparten la misma base, exactamente como
ya se hace con `product_sales`. Sin índice nuevo (los joins son por PK de `products`/`categories`).

---

## 2. Insight 5 — Afinidad de canasta por categoría

**Se agrega** la vista "qué categorías se compran juntas" a `/insights/basket-affinity`, **sin** tocar
los pares de producto (§5.5 original). **Mismo método**: co-ocurrencia (soporte) como umbral + **lift**
como fuerza; se agrupa por categoría en vez de por producto.

### 2.1 Decisión clave: venta con 2 líneas de la MISMA categoría, productos distintos

**Se EXCLUYE como afinidad categoría-consigo-misma.** Fundamento: la afinidad mide **categorías
distintas** compradas en la misma venta (como los pares de producto excluyen el auto-par vía
`product_id < product_id`). Se implementa colapsando la venta a sus **categorías DISTINTAS** con
`SELECT DISTINCT (sale_id, categoría)`: dos productos de la misma categoría producen **una sola**
fila-categoría en esa venta, por lo que el self-join `a.cat_key < b.cat_key` nunca la puede emparejar
consigo misma. Resultado: "Croissant + Medialuna" (ambos en "Panadería") **no** genera el par
Panadería–Panadería; solo cuenta como presencia de Panadería en la venta (para el denominador del
lift). Esto es intencional y consistente con el método de producto.

### 2.2 Query

```sql
WITH sale_categories AS (                    -- categorías DISTINTAS por venta del período
  SELECT DISTINCT si.sale_id,
         COALESCE(c.id::text, 'none')   AS cat_key,   -- clave estable por id (no por nombre)
         COALESCE(c.name, 'Sin categoría') AS cat_name
  FROM sale_items si
  JOIN sales s        ON s.id = si.sale_id
  LEFT JOIN products p   ON p.id = si.product_id
  LEFT JOIN categories c ON c.id = p.category_id
  WHERE s.tenant_id = $1 AND s.created_at >= $2 AND s.created_at < $3 /*[branch s.]*/
),
pairs AS (
  SELECT a.cat_key AS ka, a.cat_name AS na, b.cat_key AS kb, b.cat_name AS nb,
         COUNT(*) AS support               -- nº de ventas que contienen AMBAS categorías
  FROM sale_categories a
  JOIN sale_categories b
    ON a.sale_id = b.sale_id
   AND a.cat_key < b.cat_key               -- evita par duplicado y auto-par (categoría-consigo-misma)
  GROUP BY a.cat_key, a.cat_name, b.cat_key, b.cat_name
  HAVING COUNT(*) >= $4                      -- soporte mínimo (default 3, reusa defaultAffinitySupportMin)
),
freq AS (SELECT cat_key, COUNT(*) AS cnt FROM sale_categories GROUP BY cat_key),
tot  AS (SELECT COUNT(DISTINCT sale_id) AS n FROM sale_categories)
SELECT pairs.na, pairs.nb, pairs.support,
       (pairs.support::numeric * tot.n) / (fa.cnt * fb.cnt) AS lift
FROM pairs
JOIN freq fa ON fa.cat_key = pairs.ka
JOIN freq fb ON fb.cat_key = pairs.kb
CROSS JOIN tot
ORDER BY pairs.support DESC, lift DESC
LIMIT 10;
```

### 2.3 Tratamiento de bordes (decisiones explícitas)

- **Clave por `c.id::text`, no por nombre.** A diferencia del ranking (§1, que agrupa por nombre para
  fusionar "Sin categoría"), aquí la clave de agrupamiento/dedup es el **id** de categoría. Evita que
  dos categorías con el mismo nombre se fusionen y da un `<` estable para el self-join. El nombre solo
  se arrastra para mostrar (es funcionalmente dependiente de `cat_key`, así que la agrupación es
  determinística). `cat_name` se muestra tal cual.
- **"Sin categoría" (`cat_key='none'`) SÍ participa como bucket** en el emparejamiento (default). Es
  un grupo coherente (a diferencia de un producto borrado suelto, que en §5.5 de producto se descarta
  porque no se puede identificar el par). Un producto sin categoría + uno categorizado en la misma
  venta genera el par `("Panadería", "Sin categoría")`. Dos productos sin categoría colapsan a un solo
  nodo "Sin categoría" (por el `DISTINCT`), así que tampoco se auto-emparejan. → **Ver §4 P1:** si
  Silvin prefiere ocultar los pares con "Sin categoría" por ser poco accionables, es un filtro trivial
  (`WHERE cat_key <> 'none'` en `sale_categories`) — es decisión de producto, no técnica.
- **Producto borrado** (`product_id`/producto ausente) → `cat_key='none'` → entra en "Sin categoría"
  (mismo destino que en el ranking). No se descarta como en la afinidad de producto, porque a nivel
  categoría el bucket sí es identificable.
- **Anónimas cuentan** (nivel venta), igual que la afinidad de producto.
- **Insuficiente:** si `pairs` sale vacío → `categoryInsufficient = true` (flag separado del de
  producto; ver §3). Sub-caso esperado: un negocio con **una sola** categoría real nunca produce
  pares → `categoryInsufficient=true` legítimamente (la UI muestra "muestra insuficiente", no error).

### 2.4 Nota de complejidad

Igual argumento que §5.5 original: el self-join es `Σ_ventas (m_i choose 2)` con `m_i` = categorías
DISTINTAS por venta. `m_i` es **aún más chico** que el de productos (varios productos colapsan a una
categoría), así que esta query es más barata que la de pares de producto. Sin precómputo, sin índice
nuevo. Se mantiene el cálculo en vivo (ADR-009).

---

## 3. Cambios de contrato de API (aditivos, no rompen nada)

**Se extienden los 2 endpoints existentes; NO se crean rutas nuevas.** Los campos son aditivos: un
cliente que ignore los campos nuevos sigue funcionando. Mismos query params (`from/to/tz/branchId`),
mismo gating (§2 de la spec).

### 3.1 `GET /insights/top-products` — campos nuevos

```jsonc
{
  "byRevenue":          [ /* … sin cambios … */ ],
  "byVolume":           [ /* … sin cambios … */ ],
  "byMargin":           [ /* … sin cambios … */ ],
  "excludedFromMargin": [ /* … sin cambios … */ ],
  // NUEVO:
  "byCategoryRevenue": [ { "name": "Panadería", "revenueCents": 45200, "units": 130 } ],
  "byCategoryVolume":  [ { "name": "Café",      "revenueCents": 38000, "units": 210 } ]
}
```
No se agrega `byCategoryMargin` (margen por categoría fuera de alcance, §0).

### 3.2 `GET /insights/basket-affinity` — campos nuevos

```jsonc
{
  "insufficient": false,
  "items":        [ /* pares de PRODUCTO, sin cambios … */ ],
  // NUEVO — pares de CATEGORÍA (independientes de los de producto):
  "categoryItems":        [ { "a": "Café", "b": "Panadería", "support": 42, "lift": 1.8 } ],
  "categoryInsufficient": false
}
```
`categoryInsufficient` es **independiente** de `insufficient`: puede haber pares de categoría aunque no
haya suficientes pares de producto (o viceversa). La UI evalúa cada bloque por su propio flag.

---

## 4. Cambios en `model.go` (structs Go)

Aditivos. No se modifican los tipos existentes salvo por los campos nuevos en los dos structs de
respuesta.

```go
// ---- Insight 2 (§5.2 + addendum) ----

// CategoryRevenue es una fila de los rankings por categoría (ingresos y volumen).
// name = nombre de categoría o "Sin categoría" (bucket que agrupa productos sin
// categoría y productos borrados; ver addendum §1.2).
type CategoryRevenue struct {
	Name         string `json:"name"`
	RevenueCents int    `json:"revenueCents"`
	Units        int    `json:"units"`
}

// TopProductsInsight: agregar estos dos campos al struct existente.
type TopProductsInsight struct {
	ByRevenue          []ProductRevenue  `json:"byRevenue"`
	ByVolume           []ProductRevenue  `json:"byVolume"`
	ByMargin           []ProductMargin   `json:"byMargin"`
	ExcludedFromMargin []ExcludedProduct `json:"excludedFromMargin"`
	ByCategoryRevenue  []CategoryRevenue `json:"byCategoryRevenue"` // NUEVO
	ByCategoryVolume   []CategoryRevenue `json:"byCategoryVolume"`  // NUEVO
}

// ---- Insight 5 (§5.5 + addendum) ----

// CategoryPair es un par de categorías co-compradas en la misma venta. Support = nº
// de ventas que contienen AMBAS categorías (distintas); Lift = (support × N) /
// (freq_A × freq_B). No incluye pares categoría-consigo-misma (addendum §2.1).
type CategoryPair struct {
	A       string  `json:"a"`
	B       string  `json:"b"`
	Support int     `json:"support"`
	Lift    float64 `json:"lift"`
}

// BasketAffinityInsight: agregar estos dos campos al struct existente.
type BasketAffinityInsight struct {
	Insufficient         bool           `json:"insufficient"`
	Items                []BasketPair   `json:"items"`
	CategoryItems        []CategoryPair `json:"categoryItems"`        // NUEVO
	CategoryInsufficient bool           `json:"categoryInsufficient"` // NUEVO
}
```

- El backend debe **inicializar los slices nuevos como `[]CategoryRevenue{}` / `[]CategoryPair{}`**
  (no `nil`), igual que hoy hace `topProducts`/`basketAffinity`, para que serialicen `[]` y no `null`.
- `store.basketAffinity` reusa `defaultAffinitySupportMin` (mismo soporte mínimo que los pares de
  producto) para `categoryItems`; `categoryInsufficient = len(categoryItems) == 0`.

---

## 5. Qué SÍ requiere confirmación de producto (no técnica)

- **P1 — ¿Mostrar pares de afinidad que incluyen "Sin categoría"?** Default implementado: **sí se
  muestran** (bucket coherente, consistente con el ranking y con `reports`). Si Silvin los considera
  ruido poco accionable ("Café + Sin categoría" no sugiere una acción comercial clara), ocultarlos es
  un filtro de una línea (`WHERE cat_key <> 'none'`). No bloquea la implementación; se puede activar
  después sin cambiar contrato. **Recomendación:** dejar el default (mostrar) y confirmar con Silvin al
  ver la vista en vivo — mismo modo en que surgió esta extensión.

Todo lo demás (self-pair, producto borrado, "Sin categoría" como bucket en el ranking, margen fuera)
es **decisión técnica ya tomada** en este addendum, consistente con precedentes en prod.

---

## 6. Handoff

- **→ backend-engineer:** agregar `store.categoryRankings` (o extender `topProducts` con las 2 queries
  de §1) y extender `store.basketAffinity` con la query de §2; ampliar los 2 structs de `model.go`
  (§4). Reusa `branchClause("s.", 4, ...)` y los defaults existentes. Sin migración, sin índice nuevo.
  Tests nuevos: (a) ranking de categoría con bucket "Sin categoría" incluyendo un producto borrado;
  (b) afinidad de categoría donde una venta con 2 productos de la misma categoría **no** genera
  self-par; (c) `categoryInsufficient=true` con una sola categoría.
- **→ frontend-engineer:** los 2 endpoints devuelven campos nuevos aditivos (§3). Renderizar 2
  rankings más en la tarjeta de Insight 2 (ingresos/volumen por categoría, sin margen) y un bloque de
  pares de categoría en Insight 5, gobernado por `categoryInsufficient`. Sin rutas nuevas.
- **→ devops-engineer:** sin acción (sin infra, sin migración, sin variables).
