package insights

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// store hace SOLO lectura sobre las tablas existentes (sales, sale_items,
// products, product_supplies, supplies, loyalty_redemptions). No tiene Exec,
// Begin ni transacciones: el módulo no muta nada (tech-spec §6, ADR-009).
type store struct {
	pool *pgxpool.Pool
}

func newStore(pool *pgxpool.Pool) *store {
	return &store{pool: pool}
}

// branchClause devuelve el fragmento SQL y los args extra para el filtro por
// sucursal. alias es el prefijo de la columna (p. ej. "" o "s."). nextIdx es el
// índice del próximo placeholder posicional disponible. Copiado tal cual de
// reports.branchClause (mismo contrato). El placeholder $nextIdx puede reutilizarse
// en varias posiciones de la misma query apuntando al mismo arg.
func branchClause(alias string, nextIdx int, f BranchFilter) (string, []any) {
	switch {
	case f.None:
		return fmt.Sprintf(" AND %sbranch_id IS NULL", alias), nil
	case f.ID != nil:
		return fmt.Sprintf(" AND %sbranch_id = $%d", alias, nextIdx), []any{*f.ID}
	default:
		return "", nil
	}
}

// ---- Insight 1: Recurrencia (§5.1) ----------------------------------------

// recurrenceRaw son los conteos crudos que trae el store; el service deriva
// porcentajes/tasa/empty en Go.
type recurrenceRaw struct {
	WithGe1    int
	WithGe2    int
	AnonSales  int
	TotalSales int
	Cohort     RecurrenceCohort
}

func (s *store) recurrence(ctx context.Context, sc Scope) (recurrenceRaw, error) {
	var raw recurrenceRaw

	// (a) Tasa de recurrencia — solo identificados en el rango.
	bc, bArgs := branchClause("", 4, sc.Branch)
	args := append([]any{sc.TenantID, sc.From, sc.To}, bArgs...)
	if err := s.pool.QueryRow(ctx, `
		WITH per_customer AS (
		  SELECT customer_id, COUNT(*) AS n
		  FROM sales
		  WHERE tenant_id = $1 AND created_at >= $2 AND created_at < $3
		    AND customer_id IS NOT NULL`+bc+`
		  GROUP BY customer_id
		)
		SELECT COUNT(*) AS with_ge1,
		       COUNT(*) FILTER (WHERE n >= 2) AS with_ge2
		FROM per_customer`,
		args...).Scan(&raw.WithGe1, &raw.WithGe2); err != nil {
		return recurrenceRaw{}, err
	}

	// (b) Bucket anónimas — conteo y total de ventas del período.
	bcB, bArgsB := branchClause("", 4, sc.Branch)
	argsB := append([]any{sc.TenantID, sc.From, sc.To}, bArgsB...)
	if err := s.pool.QueryRow(ctx, `
		SELECT COUNT(*) FILTER (WHERE customer_id IS NULL) AS anon_sales,
		       COUNT(*)                                    AS total_sales
		FROM sales
		WHERE tenant_id = $1 AND created_at >= $2 AND created_at < $3`+bcB,
		argsB...).Scan(&raw.AnonSales, &raw.TotalSales); err != nil {
		return recurrenceRaw{}, err
	}

	// (c) Cohorte de nuevos 30/60/90 — primera venta histórica en [from,to); el
	// retorno mira el historial real (más allá de `to`). El filtro [branch] se
	// aplica uniforme a first_sale (alias "") y a los EXISTS (alias "s."), ambos
	// reutilizando el mismo placeholder $4.
	bcEmpty, bArgsC := branchClause("", 4, sc.Branch)
	bcS, _ := branchClause("s.", 4, sc.Branch)
	argsC := append([]any{sc.TenantID, sc.From, sc.To}, bArgsC...)
	if err := s.pool.QueryRow(ctx, `
		WITH first_sale AS (
		  SELECT customer_id, MIN(created_at) AS first_at
		  FROM sales
		  WHERE tenant_id = $1 AND customer_id IS NOT NULL`+bcEmpty+`
		  GROUP BY customer_id
		),
		cohort AS (
		  SELECT customer_id, first_at FROM first_sale
		  WHERE first_at >= $2 AND first_at < $3
		)
		SELECT
		  COUNT(*) AS cohort_n,
		  COUNT(*) FILTER (WHERE EXISTS (
		    SELECT 1 FROM sales s WHERE s.tenant_id = $1 AND s.customer_id = c.customer_id`+bcS+`
		      AND s.created_at >  c.first_at
		      AND s.created_at <= c.first_at + INTERVAL '30 days')) AS ret30,
		  COUNT(*) FILTER (WHERE EXISTS (
		    SELECT 1 FROM sales s WHERE s.tenant_id = $1 AND s.customer_id = c.customer_id`+bcS+`
		      AND s.created_at >  c.first_at
		      AND s.created_at <= c.first_at + INTERVAL '60 days')) AS ret60,
		  COUNT(*) FILTER (WHERE EXISTS (
		    SELECT 1 FROM sales s WHERE s.tenant_id = $1 AND s.customer_id = c.customer_id`+bcS+`
		      AND s.created_at >  c.first_at
		      AND s.created_at <= c.first_at + INTERVAL '90 days')) AS ret90
		FROM cohort c`,
		argsC...).Scan(&raw.Cohort.N, &raw.Cohort.Ret30, &raw.Cohort.Ret60, &raw.Cohort.Ret90); err != nil {
		return recurrenceRaw{}, err
	}

	return raw, nil
}

// ---- Insight 2: Producto estrella (§5.2) ----------------------------------

// productSalesCTE es la base de ventas por producto (ingresos + volumen; anónimas
// cuentan a nivel línea). Se comparte entre los rankings. El filtro [branch] usa
// alias "s." en $4.
const productSalesCTE = `
	WITH product_sales AS (
	  SELECT si.product_id,
	         COALESCE(p.name, si.name, 'Producto eliminado') AS name,
	         SUM(si.line_total_cents) AS revenue_cents,
	         SUM(si.quantity)         AS units
	  FROM sale_items si
	  JOIN sales s ON s.id = si.sale_id
	  LEFT JOIN products p ON p.id = si.product_id
	  WHERE s.tenant_id = $1 AND s.created_at >= $2 AND s.created_at < $3%s
	  GROUP BY si.product_id, COALESCE(p.name, si.name, 'Producto eliminado')
	)`

// categorySalesCTE es la base de ventas por categoría (ingresos + volumen). Une
// sale_items → products → categories con LEFT JOIN, igual que reports.salesReport:
// "Sin categoría" (COALESCE) agrupa productos vivos sin categoría y productos
// borrados (addendum §1.2). El filtro [branch] usa alias "s." en $4.
const categorySalesCTE = `
	WITH category_sales AS (
	  SELECT COALESCE(c.name, 'Sin categoría') AS name,
	         SUM(si.line_total_cents)          AS revenue_cents,
	         SUM(si.quantity)                  AS units
	  FROM sale_items si
	  JOIN sales s ON s.id = si.sale_id
	  LEFT JOIN products p ON p.id = si.product_id
	  LEFT JOIN categories c ON c.id = p.category_id
	  WHERE s.tenant_id = $1 AND s.created_at >= $2 AND s.created_at < $3%s
	  GROUP BY COALESCE(c.name, 'Sin categoría')
	)`

// recipeCostCTE calcula el costo de insumos por unidad vendida del producto. Usa
// bool_or(package_cost_cents IS NULL) para detectar costo faltante: SUM() ignora
// los NULL, así que confiar en la suma subestimaría el costo (tech-spec T4/F7).
const recipeCostCTE = `
	recipe_cost AS (
	  SELECT ps.product_id,
	         bool_or(sp.package_cost_cents IS NULL) AS has_null_cost,
	         SUM( (ps.quantity_base::numeric * sp.package_cost_cents) / sp.package_content )
	             AS unit_cost_cents
	  FROM product_supplies ps
	  JOIN supplies sp ON sp.id = ps.supply_id
	  WHERE ps.tenant_id = $1
	  GROUP BY ps.product_id
	)`

func (s *store) topProducts(ctx context.Context, sc Scope) (TopProductsInsight, error) {
	out := TopProductsInsight{
		ByRevenue:          []ProductRevenue{},
		ByVolume:           []ProductRevenue{},
		ByMargin:           []ProductMargin{},
		ExcludedFromMargin: []ExcludedProduct{},
		ByCategoryRevenue:  []CategoryRevenue{},
		ByCategoryVolume:   []CategoryRevenue{},
	}
	bc, bArgs := branchClause("s.", 4, sc.Branch)
	args := append([]any{sc.TenantID, sc.From, sc.To}, bArgs...)
	base := fmt.Sprintf(productSalesCTE, bc)

	// Ranking por ingresos.
	revRows, err := s.pool.Query(ctx, base+`
		SELECT name, revenue_cents, units FROM product_sales
		ORDER BY revenue_cents DESC LIMIT 5`, args...)
	if err != nil {
		return TopProductsInsight{}, err
	}
	for revRows.Next() {
		var p ProductRevenue
		if err := revRows.Scan(&p.Name, &p.RevenueCents, &p.Units); err != nil {
			revRows.Close()
			return TopProductsInsight{}, err
		}
		out.ByRevenue = append(out.ByRevenue, p)
	}
	revRows.Close()
	if err := revRows.Err(); err != nil {
		return TopProductsInsight{}, err
	}

	// Ranking por volumen.
	volRows, err := s.pool.Query(ctx, base+`
		SELECT name, revenue_cents, units FROM product_sales
		ORDER BY units DESC LIMIT 5`, args...)
	if err != nil {
		return TopProductsInsight{}, err
	}
	for volRows.Next() {
		var p ProductRevenue
		if err := volRows.Scan(&p.Name, &p.RevenueCents, &p.Units); err != nil {
			volRows.Close()
			return TopProductsInsight{}, err
		}
		out.ByVolume = append(out.ByVolume, p)
	}
	volRows.Close()
	if err := volRows.Err(); err != nil {
		return TopProductsInsight{}, err
	}

	// Ranking por margen: excluye productos con algún insumo sin costo (has_null_cost)
	// y productos sin receta (el JOIN los descarta).
	marginRows, err := s.pool.Query(ctx, base+`,`+recipeCostCTE+`
		SELECT ps.name,
		       ps.revenue_cents,
		       ps.revenue_cents - ROUND(rc.unit_cost_cents * ps.units)::bigint AS margin_cents
		FROM product_sales ps
		JOIN recipe_cost rc ON rc.product_id = ps.product_id
		WHERE rc.has_null_cost = false
		ORDER BY margin_cents DESC
		LIMIT 5`, args...)
	if err != nil {
		return TopProductsInsight{}, err
	}
	for marginRows.Next() {
		var p ProductMargin
		if err := marginRows.Scan(&p.Name, &p.RevenueCents, &p.MarginCents); err != nil {
			marginRows.Close()
			return TopProductsInsight{}, err
		}
		out.ByMargin = append(out.ByMargin, p)
	}
	marginRows.Close()
	if err := marginRows.Err(); err != nil {
		return TopProductsInsight{}, err
	}

	// Excluidos del margen: borrado (product_id NULL), sin receta (rc.product_id
	// NULL) o receta con insumo sin costo (has_null_cost).
	exclRows, err := s.pool.Query(ctx, base+`,`+recipeCostCTE+`
		SELECT ps.name,
		       CASE WHEN rc.product_id IS NULL THEN 'no_recipe' ELSE 'null_cost' END AS reason
		FROM product_sales ps
		LEFT JOIN recipe_cost rc ON rc.product_id = ps.product_id
		WHERE ps.product_id IS NULL
		   OR rc.product_id IS NULL
		   OR rc.has_null_cost = true
		ORDER BY ps.revenue_cents DESC`, args...)
	if err != nil {
		return TopProductsInsight{}, err
	}
	for exclRows.Next() {
		var e ExcludedProduct
		if err := exclRows.Scan(&e.Name, &e.Reason); err != nil {
			exclRows.Close()
			return TopProductsInsight{}, err
		}
		out.ExcludedFromMargin = append(out.ExcludedFromMargin, e)
	}
	exclRows.Close()
	if err := exclRows.Err(); err != nil {
		return TopProductsInsight{}, err
	}

	// Rankings por categoría (ingresos y volumen). Misma base category_sales, dos
	// ordenamientos, reusando los mismos args (tenant, from, to (+branch)). Addendum §1.
	catBase := fmt.Sprintf(categorySalesCTE, bc)

	catRevRows, err := s.pool.Query(ctx, catBase+`
		SELECT name, revenue_cents, units FROM category_sales
		ORDER BY revenue_cents DESC LIMIT 5`, args...)
	if err != nil {
		return TopProductsInsight{}, err
	}
	for catRevRows.Next() {
		var c CategoryRevenue
		if err := catRevRows.Scan(&c.Name, &c.RevenueCents, &c.Units); err != nil {
			catRevRows.Close()
			return TopProductsInsight{}, err
		}
		out.ByCategoryRevenue = append(out.ByCategoryRevenue, c)
	}
	catRevRows.Close()
	if err := catRevRows.Err(); err != nil {
		return TopProductsInsight{}, err
	}

	catVolRows, err := s.pool.Query(ctx, catBase+`
		SELECT name, revenue_cents, units FROM category_sales
		ORDER BY units DESC LIMIT 5`, args...)
	if err != nil {
		return TopProductsInsight{}, err
	}
	for catVolRows.Next() {
		var c CategoryRevenue
		if err := catVolRows.Scan(&c.Name, &c.RevenueCents, &c.Units); err != nil {
			catVolRows.Close()
			return TopProductsInsight{}, err
		}
		out.ByCategoryVolume = append(out.ByCategoryVolume, c)
	}
	catVolRows.Close()
	if err := catVolRows.Err(); err != nil {
		return TopProductsInsight{}, err
	}

	return out, nil
}

// ---- Insight 3: Ticket por segmento (§5.3) --------------------------------

// ticketRaw son las sumas/conteos crudos por segmento; el service deriva el ticket
// promedio con guarda de división.
type ticketRaw struct {
	RecSpend  int
	RecSales  int
	NewSpend  int
	NewSales  int
	AnonSales int
	AnonSpend int
}

func (s *store) ticketSegments(ctx context.Context, sc Scope) (ticketRaw, error) {
	var raw ticketRaw
	// El filtro [branch] aparece en per_customer y anon (ambos alias "", $4).
	bc, bArgs := branchClause("", 4, sc.Branch)
	args := append([]any{sc.TenantID, sc.From, sc.To}, bArgs...)
	if err := s.pool.QueryRow(ctx, `
		WITH per_customer AS (
		  SELECT customer_id, COUNT(*) AS n, SUM(total_cents) AS spend
		  FROM sales
		  WHERE tenant_id = $1 AND created_at >= $2 AND created_at < $3
		    AND customer_id IS NOT NULL`+bc+`
		  GROUP BY customer_id
		),
		anon AS (
		  SELECT COUNT(*) AS sales_n, COALESCE(SUM(total_cents),0) AS spend
		  FROM sales
		  WHERE tenant_id = $1 AND created_at >= $2 AND created_at < $3
		    AND customer_id IS NULL`+bc+`
		)
		SELECT
		  COALESCE(SUM(spend) FILTER (WHERE n >= 2), 0)::bigint AS rec_spend,
		  COALESCE(SUM(n)     FILTER (WHERE n >= 2), 0)::bigint AS rec_sales,
		  COALESCE(SUM(spend) FILTER (WHERE n = 1),  0)::bigint AS new_spend,
		  COALESCE(SUM(n)     FILTER (WHERE n = 1),  0)::bigint AS new_sales,
		  (SELECT sales_n FROM anon)::bigint AS anon_sales,
		  (SELECT spend   FROM anon)::bigint AS anon_spend
		FROM per_customer`,
		args...).Scan(&raw.RecSpend, &raw.RecSales, &raw.NewSpend, &raw.NewSales, &raw.AnonSales, &raw.AnonSpend); err != nil {
		return ticketRaw{}, err
	}
	return raw, nil
}

// ---- Insight 4: 2ª visita (§5.4) ------------------------------------------

func (s *store) secondVisit(ctx context.Context, sc Scope, sampleMin, supportMin int) (SecondVisitInsight, error) {
	out := SecondVisitInsight{Items: []SecondVisitItem{}}

	// (1) Tamaño de muestra: nº de 2ª visitas cuyo evento cae en el rango.
	// Numeración GLOBAL sobre el historial completo del cliente.
	bc, bArgs := branchClause("s.", 4, sc.Branch)
	args := append([]any{sc.TenantID, sc.From, sc.To}, bArgs...)
	if err := s.pool.QueryRow(ctx, `
		WITH visit_seq AS (
		  SELECT s.id AS sale_id, s.customer_id,
		         ROW_NUMBER() OVER (PARTITION BY s.customer_id ORDER BY s.created_at, s.id) AS visit_no
		  FROM sales s
		  WHERE s.tenant_id = $1 AND s.customer_id IS NOT NULL`+bc+`
		),
		period_sales AS (
		  SELECT vs.sale_id, vs.visit_no
		  FROM visit_seq vs
		  JOIN sales s ON s.id = vs.sale_id
		  WHERE s.created_at >= $2 AND s.created_at < $3
		)
		SELECT COUNT(*) FILTER (WHERE visit_no = 2) AS n2_total
		FROM period_sales`,
		args...).Scan(&out.SampleSize); err != nil {
		return SecondVisitInsight{}, err
	}

	// Muestra insuficiente: sin lista (handoff §3.4).
	if out.SampleSize < sampleMin {
		out.Insufficient = true
		return out, nil
	}

	// (2) Productos sobre-representados en la 2ª visita. El soporte mínimo por
	// producto va tras el arg de sucursal (si lo hay).
	bc2, bArgs2 := branchClause("s.", 4, sc.Branch)
	supIdx := 4 + len(bArgs2)
	args2 := append([]any{sc.TenantID, sc.From, sc.To}, bArgs2...)
	args2 = append(args2, supportMin)
	rows, err := s.pool.Query(ctx, `
		WITH visit_seq AS (
		  SELECT s.id AS sale_id, s.customer_id,
		         ROW_NUMBER() OVER (PARTITION BY s.customer_id ORDER BY s.created_at, s.id) AS visit_no
		  FROM sales s
		  WHERE s.tenant_id = $1 AND s.customer_id IS NOT NULL`+bc2+`
		),
		period_sales AS (
		  SELECT vs.sale_id, vs.visit_no
		  FROM visit_seq vs
		  JOIN sales s ON s.id = vs.sale_id
		  WHERE s.created_at >= $2 AND s.created_at < $3
		),
		n2 AS (SELECT sale_id FROM period_sales WHERE visit_no = 2),
		tot AS (
		  SELECT (SELECT COUNT(*) FROM n2)           AS n2_total,
		         (SELECT COUNT(*) FROM period_sales) AS all_total
		),
		prod2 AS (
		  SELECT si.product_id, COALESCE(p.name, si.name) AS name,
		         COUNT(DISTINCT si.sale_id) AS cnt2
		  FROM sale_items si
		  JOIN n2 ON n2.sale_id = si.sale_id
		  LEFT JOIN products p ON p.id = si.product_id
		  WHERE si.product_id IS NOT NULL
		  GROUP BY si.product_id, COALESCE(p.name, si.name)
		),
		prodall AS (
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
		WHERE prod2.cnt2 >= $`+fmt.Sprint(supIdx)+`
		ORDER BY over_rep DESC NULLS LAST
		LIMIT 10`, args2...)
	if err != nil {
		return SecondVisitInsight{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var it SecondVisitItem
		var overRep *float64
		if err := rows.Scan(&it.Name, &it.Support, &it.Of, &overRep); err != nil {
			return SecondVisitInsight{}, err
		}
		if overRep != nil {
			it.OverRep = *overRep
		}
		out.Items = append(out.Items, it)
	}
	return out, rows.Err()
}

// ---- Insight 5: Afinidad de canasta (§5.5) --------------------------------

func (s *store) basketAffinity(ctx context.Context, sc Scope, supportMin int) (BasketAffinityInsight, error) {
	out := BasketAffinityInsight{Items: []BasketPair{}, CategoryItems: []CategoryPair{}}

	// El soporte mínimo (HAVING) va tras el arg de sucursal (si lo hay).
	bc, bArgs := branchClause("s.", 4, sc.Branch)
	supIdx := 4 + len(bArgs)
	args := append([]any{sc.TenantID, sc.From, sc.To}, bArgs...)
	args = append(args, supportMin)
	rows, err := s.pool.Query(ctx, `
		WITH sale_products AS (
		  SELECT DISTINCT si.sale_id, si.product_id, COALESCE(p.name, si.name) AS name
		  FROM sale_items si
		  JOIN sales s ON s.id = si.sale_id
		  LEFT JOIN products p ON p.id = si.product_id
		  WHERE s.tenant_id = $1 AND s.created_at >= $2 AND s.created_at < $3`+bc+`
		    AND si.product_id IS NOT NULL
		),
		pairs AS (
		  SELECT a.product_id AS pa, a.name AS na, b.product_id AS pb, b.name AS nb,
		         COUNT(*) AS support
		  FROM sale_products a
		  JOIN sale_products b
		    ON a.sale_id = b.sale_id
		   AND a.product_id < b.product_id
		  GROUP BY a.product_id, a.name, b.product_id, b.name
		  HAVING COUNT(*) >= $`+fmt.Sprint(supIdx)+`
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
		LIMIT 10`, args...)
	if err != nil {
		return BasketAffinityInsight{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var p BasketPair
		if err := rows.Scan(&p.A, &p.B, &p.Support, &p.Lift); err != nil {
			return BasketAffinityInsight{}, err
		}
		out.Items = append(out.Items, p)
	}
	if err := rows.Err(); err != nil {
		return BasketAffinityInsight{}, err
	}
	out.Insufficient = len(out.Items) == 0

	// Afinidad por categoría (addendum §2). Mismo método (soporte + lift) sobre las
	// categorías DISTINTAS de cada venta: dos productos de la misma categoría colapsan
	// a un solo nodo (DISTINCT), así que el self-join a.cat_key < b.cat_key nunca los
	// empareja consigo mismos (§2.1). Clave por c.id::text ('none' = "Sin categoría",
	// bucket que participa como nodo, §2.3). Reusa defaultAffinitySupportMin en $supIdx.
	bc2, bArgs2 := branchClause("s.", 4, sc.Branch)
	supIdx2 := 4 + len(bArgs2)
	args2 := append([]any{sc.TenantID, sc.From, sc.To}, bArgs2...)
	args2 = append(args2, supportMin)
	catRows, err := s.pool.Query(ctx, `
		WITH sale_categories AS (
		  SELECT DISTINCT si.sale_id,
		         COALESCE(c.id::text, 'none')      AS cat_key,
		         COALESCE(c.name, 'Sin categoría') AS cat_name
		  FROM sale_items si
		  JOIN sales s ON s.id = si.sale_id
		  LEFT JOIN products p ON p.id = si.product_id
		  LEFT JOIN categories c ON c.id = p.category_id
		  WHERE s.tenant_id = $1 AND s.created_at >= $2 AND s.created_at < $3`+bc2+`
		),
		pairs AS (
		  SELECT a.cat_key AS ka, a.cat_name AS na, b.cat_key AS kb, b.cat_name AS nb,
		         COUNT(*) AS support
		  FROM sale_categories a
		  JOIN sale_categories b
		    ON a.sale_id = b.sale_id
		   AND a.cat_key < b.cat_key
		  GROUP BY a.cat_key, a.cat_name, b.cat_key, b.cat_name
		  HAVING COUNT(*) >= $`+fmt.Sprint(supIdx2)+`
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
		LIMIT 10`, args2...)
	if err != nil {
		return BasketAffinityInsight{}, err
	}
	defer catRows.Close()
	for catRows.Next() {
		var p CategoryPair
		if err := catRows.Scan(&p.A, &p.B, &p.Support, &p.Lift); err != nil {
			return BasketAffinityInsight{}, err
		}
		out.CategoryItems = append(out.CategoryItems, p)
	}
	if err := catRows.Err(); err != nil {
		return BasketAffinityInsight{}, err
	}
	out.CategoryInsufficient = len(out.CategoryItems) == 0
	return out, nil
}

// ---- Insight 6: Efectividad de lealtad (§5.6) -----------------------------

// loyaltyRawSegment son las sumas/conteos crudos de un segmento (canjeó / no).
type loyaltyRawSegment struct {
	Redeemed   bool
	Customers  int
	SalesTotal int
	SpendTotal int
}

// loyaltyEffect segmenta por loyalty_redemptions (ADR-009 D2): un cliente "canjeó"
// si tiene >=1 fila en loyalty_redemptions atribuida a alguna de sus ventas del
// período. NO usa sales.loyalty_reward (columna inexistente). Solo identificados.
func (s *store) loyaltyEffect(ctx context.Context, sc Scope) ([]loyaltyRawSegment, error) {
	bc, bArgs := branchClause("s.", 4, sc.Branch)
	args := append([]any{sc.TenantID, sc.From, sc.To}, bArgs...)
	rows, err := s.pool.Query(ctx, `
		WITH identified AS (
		  SELECT s.id AS sale_id, s.customer_id, s.total_cents,
		         EXISTS (SELECT 1 FROM loyalty_redemptions r WHERE r.sale_id = s.id) AS redeemed_sale
		  FROM sales s
		  WHERE s.tenant_id = $1 AND s.created_at >= $2 AND s.created_at < $3
		    AND s.customer_id IS NOT NULL`+bc+`
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
		       COUNT(*)::bigint     AS customers,
		       SUM(sales_n)::bigint AS sales_total,
		       SUM(spend)::bigint   AS spend_total
		FROM cust
		GROUP BY redeemed`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var segs []loyaltyRawSegment
	for rows.Next() {
		var seg loyaltyRawSegment
		if err := rows.Scan(&seg.Redeemed, &seg.Customers, &seg.SalesTotal, &seg.SpendTotal); err != nil {
			return nil, err
		}
		segs = append(segs, seg)
	}
	return segs, rows.Err()
}

// ---- Tendencia de venta de postres (M10, F18/F19) --------------------------

// bakeryTrendRow es una fila cruda: unidades vendidas de un postre en cada ventana.
type bakeryTrendRow struct {
	Name          string
	UnitsPrevious int
	UnitsCurrent  int
}

// bakeryTrend agrega, por postre (products.fulfillment_type='bakery'), las unidades
// vendidas en la ventana anterior [prevFrom,prevTo) y en la actual [curFrom,curTo). Las
// dos ventanas son contiguas (prevTo == curFrom), así que el WHERE cubre [prevFrom,curTo).
// Solo aparecen postres con venta en alguna de las dos semanas (JOIN por sale_items).
// Orden: mayor crecimiento en volumen primero (deltaUnits DESC). SQL puro, sin IA.
func (s *store) bakeryTrend(ctx context.Context, tenantID string, prevFrom, curFrom, curTo time.Time, f BranchFilter) ([]bakeryTrendRow, error) {
	bc, bArgs := branchClause("s.", 5, f)
	args := append([]any{tenantID, prevFrom, curFrom, curTo}, bArgs...)
	rows, err := s.pool.Query(ctx, `
		SELECT p.name,
		       COALESCE(SUM(si.quantity) FILTER (WHERE s.created_at >= $2 AND s.created_at < $3), 0)::int AS units_prev,
		       COALESCE(SUM(si.quantity) FILTER (WHERE s.created_at >= $3 AND s.created_at < $4), 0)::int AS units_cur
		  FROM sale_items si
		  JOIN sales s ON s.id = si.sale_id
		  JOIN products p ON p.id = si.product_id AND p.tenant_id = $1 AND p.fulfillment_type = 'bakery'
		 WHERE s.tenant_id = $1 AND s.created_at >= $2 AND s.created_at < $4`+bc+`
		 GROUP BY p.id, p.name
		 ORDER BY (COALESCE(SUM(si.quantity) FILTER (WHERE s.created_at >= $3 AND s.created_at < $4), 0)
		           - COALESCE(SUM(si.quantity) FILTER (WHERE s.created_at >= $2 AND s.created_at < $3), 0)) DESC,
		          p.name`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []bakeryTrendRow{}
	for rows.Next() {
		var row bakeryTrendRow
		if err := rows.Scan(&row.Name, &row.UnitsPrevious, &row.UnitsCurrent); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}
