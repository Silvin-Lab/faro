// Package insights produce métricas de comportamiento (solo lectura) sobre las
// ventas del negocio: recurrencia, producto estrella, ticket por segmento,
// correlación de 2ª visita, afinidad de canasta y efectividad de lealtad. Todo
// es agregación determinística SQL + aritmética Go: sin IA, sin mutaciones.
// Mismo patrón (scope/gating/branchClause) que internal/reports. Ver ADR-009.
package insights

import "time"

// Scope resume el alcance común a los 6 insights: tenant, rango [from,to), offset
// horario del cliente (tz, en minutos) y filtro por sucursal. Réplica del scope de
// reports, agrupado en un struct por claridad con 6 handlers.
type Scope struct {
	TenantID string
	From     time.Time
	To       time.Time
	TZ       int
	Branch   BranchFilter
}

// BranchFilter acota un insight a una sucursal. None = ventas sin sucursal
// (branch_id IS NULL); ID = una sucursal concreta; ambos vacíos = todas. Copiado
// de reports.BranchFilter (mismo contrato de placeholders con branchClause).
type BranchFilter struct {
	None bool
	ID   *string
}

// Umbrales calibrables con default documentado (tech-spec §5.4/§5.5, N2). No
// cambian el contrato de API; QA los ajusta sobre datos reales si hace falta.
const (
	// defaultSecondVisitSampleMin: mínimo de clientes con 2ª visita para no marcar
	// "muestra insuficiente" (Insight 4, UMBRAL_2A_VISITA).
	defaultSecondVisitSampleMin = 5
	// defaultSecondVisitSupportMin: soporte mínimo por producto para listarlo en la
	// 2ª visita (Insight 4).
	defaultSecondVisitSupportMin = 3
	// defaultAffinitySupportMin: soporte mínimo (ventas conjuntas) para mostrar un
	// par en afinidad de canasta (Insight 5).
	defaultAffinitySupportMin = 3
)

// ---- Insight 1: Recurrencia (§5.1) ----------------------------------------

// RecurrenceCohort es la cohorte de clientes nuevos (primera venta histórica en el
// rango) y cuántos volvieron dentro de 30/60/90 días (la ventana puede mirar más
// allá de `to`).
type RecurrenceCohort struct {
	N     int `json:"n"`
	Ret30 int `json:"ret30"`
	Ret60 int `json:"ret60"`
	Ret90 int `json:"ret90"`
}

// RecurrenceInsight es la respuesta de /insights/recurrence. La tasa (ratePct) se
// calcula SOLO sobre clientes identificados; el bucket anónimas va aparte y se
// devuelve aunque no haya identificados. Empty=true cuando no hay identificados.
type RecurrenceInsight struct {
	WithGe1      int              `json:"withGe1"`
	WithGe2      int              `json:"withGe2"`
	RatePct      float64          `json:"ratePct"`
	AnonSales    int              `json:"anonSales"`
	TotalSales   int              `json:"totalSales"`
	AnonSharePct float64          `json:"anonSharePct"`
	Cohort       RecurrenceCohort `json:"cohort"`
	Empty        bool             `json:"empty"`
}

// ---- Insight 2: Producto estrella (§5.2) ----------------------------------

// ProductRevenue es una fila de los rankings por ingresos y por volumen.
type ProductRevenue struct {
	Name         string `json:"name"`
	RevenueCents int    `json:"revenueCents"`
	Units        int    `json:"units"`
}

// ProductMargin es una fila del ranking por margen (ingresos − costo de receta).
type ProductMargin struct {
	Name         string `json:"name"`
	RevenueCents int    `json:"revenueCents"`
	MarginCents  int    `json:"marginCents"`
}

// ExcludedProduct es un producto que quedó fuera del ranking de margen. Reason:
// "no_recipe" (sin receta / producto borrado) o "null_cost" (algún insumo sin
// costo capturado). Nunca se asume costo 0 (tech-spec T4/F7).
type ExcludedProduct struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

// CategoryRevenue es una fila de los rankings por categoría (ingresos y volumen).
// name = nombre de categoría o "Sin categoría" (bucket que agrupa productos sin
// categoría y productos borrados; ver addendum §1.2).
type CategoryRevenue struct {
	Name         string `json:"name"`
	RevenueCents int    `json:"revenueCents"`
	Units        int    `json:"units"`
}

// TopProductsInsight es la respuesta de /insights/top-products. Los rankings por
// categoría (byCategory*) son aditivos al contrato original (addendum §1/§3.1);
// margen por categoría queda fuera de v1 (addendum §0).
type TopProductsInsight struct {
	ByRevenue          []ProductRevenue  `json:"byRevenue"`
	ByVolume           []ProductRevenue  `json:"byVolume"`
	ByMargin           []ProductMargin   `json:"byMargin"`
	ExcludedFromMargin []ExcludedProduct `json:"excludedFromMargin"`
	ByCategoryRevenue  []CategoryRevenue `json:"byCategoryRevenue"`
	ByCategoryVolume   []CategoryRevenue `json:"byCategoryVolume"`
}

// ---- Insight 3: Ticket por segmento (§5.3) --------------------------------

// TicketSegment es el ticket promedio (centavos) y el nº de ventas de un segmento.
// AvgCents=0 cuando Sales=0 (la UI muestra "—" en ese caso; sin división en cero).
type TicketSegment struct {
	AvgCents int `json:"avgCents"`
	Sales    int `json:"sales"`
}

// TicketSegmentsInsight es la respuesta de /insights/ticket-segments. Partición
// exhaustiva y disjunta con el mismo umbral que Insight 1 (recurrente = n≥2, nuevo
// = n=1, anónimas = customer_id NULL).
type TicketSegmentsInsight struct {
	New       TicketSegment `json:"new"`
	Recurring TicketSegment `json:"recurring"`
	Anonymous TicketSegment `json:"anonymous"`
}

// ---- Insight 4: 2ª visita (§5.4) ------------------------------------------

// SecondVisitItem es un producto sobre-representado en la 2ª visita. OverRep =
// razón de sobre-representación (share en visita#2 / share global). Support = nº
// de 2ª visitas que incluyen el producto. Of = tamaño de muestra (n2_total).
type SecondVisitItem struct {
	Name    string  `json:"name"`
	OverRep float64 `json:"overRep"`
	Support int     `json:"support"`
	Of      int     `json:"of"`
}

// SecondVisitInsight es la respuesta de /insights/second-visit. Insufficient=true
// (sin lista) cuando SampleSize < umbral de muestra.
type SecondVisitInsight struct {
	Insufficient bool              `json:"insufficient"`
	SampleSize   int               `json:"sampleSize"`
	Items        []SecondVisitItem `json:"items"`
}

// ---- Insight 5: Afinidad de canasta (§5.5) --------------------------------

// BasketPair es un par de productos co-comprados. Support = nº de ventas que
// contienen ambos; Lift = fuerza normalizada (support × N) / (freq_A × freq_B).
type BasketPair struct {
	A       string  `json:"a"`
	B       string  `json:"b"`
	Support int     `json:"support"`
	Lift    float64 `json:"lift"`
}

// CategoryPair es un par de categorías co-compradas en la misma venta. Support =
// nº de ventas que contienen AMBAS categorías (distintas); Lift = (support × N) /
// (freq_A × freq_B). No incluye pares categoría-consigo-misma (addendum §2.1).
type CategoryPair struct {
	A       string  `json:"a"`
	B       string  `json:"b"`
	Support int     `json:"support"`
	Lift    float64 `json:"lift"`
}

// BasketAffinityInsight es la respuesta de /insights/basket-affinity.
// Insufficient=true (lista vacía) cuando no hay pares que alcancen el soporte.
// CategoryItems/CategoryInsufficient son la vista aditiva por categoría (addendum
// §2/§3.2); CategoryInsufficient es INDEPENDIENTE de Insufficient.
type BasketAffinityInsight struct {
	Insufficient         bool           `json:"insufficient"`
	Items                []BasketPair   `json:"items"`
	CategoryItems        []CategoryPair `json:"categoryItems"`
	CategoryInsufficient bool           `json:"categoryInsufficient"`
}

// ---- Insight 6: Efectividad de lealtad (§5.6) -----------------------------

// LoyaltySegment agrega un segmento (canjeó / no canjeó). Los derivados por
// cliente se calculan en Go con guardas de división. Customers=0 cuando el
// segmento no tiene clientes (la UI conserva el otro).
type LoyaltySegment struct {
	Customers        int     `json:"customers"`
	SalesTotal       int     `json:"salesTotal"`
	SpendTotal       int     `json:"spendTotal"`
	AvgTicketCents   int     `json:"avgTicketCents"`
	SalesPerCustomer float64 `json:"salesPerCustomer"`
	SpendPerCustomer float64 `json:"spendPerCustomer"`
}

// LoyaltyEffectInsight es la respuesta de /insights/loyalty-effect. El segmento
// "canjeó" se define por loyalty_redemptions (ADR-009 D2), NO por
// sales.loyalty_reward (columna inexistente, borrada por 0010).
type LoyaltyEffectInsight struct {
	Redeemed    LoyaltySegment `json:"redeemed"`
	NotRedeemed LoyaltySegment `json:"notRedeemed"`
}

// ---- Tendencia de venta de postres (M10, F18/F19) --------------------------

// BakeryScope es el alcance del insight de repostería: tenant, offset horario (tz, en
// minutos) y filtro por sucursal. A diferencia de Scope no lleva rango: las dos ventanas
// (semana en curso vs. anterior) las calcula el service.
type BakeryScope struct {
	TenantID string
	TZ       int
	Branch   BranchFilter
}

// WeekRange es una ventana semanal [From, To) en UTC (los instantes usados en la query).
type WeekRange struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
}

// BakeryTrendItem compara la venta de un postre entre la semana anterior y la actual.
// DeltaPct es null cuando UnitsPrevious=0 (guarda de división, patrón insights).
type BakeryTrendItem struct {
	ProductName   string   `json:"productName"`
	UnitsPrevious int      `json:"unitsPrevious"`
	UnitsCurrent  int      `json:"unitsCurrent"`
	DeltaUnits    int      `json:"deltaUnits"`
	DeltaPct      *float64 `json:"deltaPct"`
}

// BakeryTrendInsight es la respuesta de /insights/bakery-trend (agregación SQL pura, sin
// IA, ADR-009). Orden: deltaUnits DESC (qué reforzar = lo que más creció en volumen).
type BakeryTrendInsight struct {
	WeekCurrent  WeekRange         `json:"weekCurrent"`
	WeekPrevious WeekRange         `json:"weekPrevious"`
	Items        []BakeryTrendItem `json:"items"`
}
