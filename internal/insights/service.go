package insights

import (
	"context"
	"math"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Service orquesta el store (SOLO lectura) y la aritmética de presentación
// (promedios, porcentajes, ratios) en Go, con guardas de división por cero. No
// llama a ningún LLM ni servicio externo (requisito no funcional duro, ADR-009).
type Service struct {
	store *store
}

func NewService(pool *pgxpool.Pool) *Service {
	return &Service{store: newStore(pool)}
}

// Recurrence (Insight 1): tasa de recurrencia SOLO sobre identificados + bucket
// anónimas separado + cohorte 30/60/90. Empty=true si no hay identificados.
func (svc *Service) Recurrence(ctx context.Context, sc Scope) (RecurrenceInsight, error) {
	raw, err := svc.store.recurrence(ctx, sc)
	if err != nil {
		return RecurrenceInsight{}, err
	}
	out := RecurrenceInsight{
		WithGe1:    raw.WithGe1,
		WithGe2:    raw.WithGe2,
		AnonSales:  raw.AnonSales,
		TotalSales: raw.TotalSales,
		Cohort:     raw.Cohort,
	}
	if raw.WithGe1 == 0 {
		out.Empty = true
	} else {
		out.RatePct = pct(raw.WithGe2, raw.WithGe1)
	}
	if raw.TotalSales > 0 {
		out.AnonSharePct = pct(raw.AnonSales, raw.TotalSales)
	}
	return out, nil
}

// TopProducts (Insight 2): 3 rankings + excluidos del margen. El store ya aplica
// bool_or para excluir costos NULL (no se asume costo 0).
func (svc *Service) TopProducts(ctx context.Context, sc Scope) (TopProductsInsight, error) {
	return svc.store.topProducts(ctx, sc)
}

// TicketSegments (Insight 3): ticket promedio por segmento (nuevo/recurrente/
// anónimas) con guarda de división (sales==0 => avgCents 0).
func (svc *Service) TicketSegments(ctx context.Context, sc Scope) (TicketSegmentsInsight, error) {
	raw, err := svc.store.ticketSegments(ctx, sc)
	if err != nil {
		return TicketSegmentsInsight{}, err
	}
	return TicketSegmentsInsight{
		New:       TicketSegment{AvgCents: avg(raw.NewSpend, raw.NewSales), Sales: raw.NewSales},
		Recurring: TicketSegment{AvgCents: avg(raw.RecSpend, raw.RecSales), Sales: raw.RecSales},
		Anonymous: TicketSegment{AvgCents: avg(raw.AnonSpend, raw.AnonSales), Sales: raw.AnonSales},
	}, nil
}

// SecondVisit (Insight 4): productos sobre-representados en la 2ª visita, con
// umbrales por default (muestra mínima y soporte por producto).
func (svc *Service) SecondVisit(ctx context.Context, sc Scope) (SecondVisitInsight, error) {
	return svc.store.secondVisit(ctx, sc, defaultSecondVisitSampleMin, defaultSecondVisitSupportMin)
}

// BasketAffinity (Insight 5): top de pares co-comprados por soporte + lift, con
// soporte mínimo por default.
func (svc *Service) BasketAffinity(ctx context.Context, sc Scope) (BasketAffinityInsight, error) {
	return svc.store.basketAffinity(ctx, sc, defaultAffinitySupportMin)
}

// LoyaltyEffect (Insight 6): comparación canjeó vs. no canjeó. Los derivados por
// cliente se calculan en Go con guardas. Segmento ausente => customers:0.
func (svc *Service) LoyaltyEffect(ctx context.Context, sc Scope) (LoyaltyEffectInsight, error) {
	segs, err := svc.store.loyaltyEffect(ctx, sc)
	if err != nil {
		return LoyaltyEffectInsight{}, err
	}
	var out LoyaltyEffectInsight
	for _, seg := range segs {
		s := LoyaltySegment{
			Customers:        seg.Customers,
			SalesTotal:       seg.SalesTotal,
			SpendTotal:       seg.SpendTotal,
			AvgTicketCents:   avg(seg.SpendTotal, seg.SalesTotal),
			SalesPerCustomer: ratio(seg.SalesTotal, seg.Customers),
			SpendPerCustomer: ratio(seg.SpendTotal, seg.Customers),
		}
		if seg.Redeemed {
			out.Redeemed = s
		} else {
			out.NotRedeemed = s
		}
	}
	return out, nil
}

// ---- Helpers de aritmética con guarda de división por cero ----------------

// pct devuelve num/den * 100 (0 si den==0).
func pct(num, den int) float64 {
	if den == 0 {
		return 0
	}
	return round2(float64(num) / float64(den) * 100)
}

// avg devuelve la división entera redondeada (centavos) de total/count (0 si
// count==0). Sin división en cero.
func avg(total, count int) int {
	if count == 0 {
		return 0
	}
	return int(math.Round(float64(total) / float64(count)))
}

// ratio devuelve num/den como float (0 si den==0).
func ratio(num, den int) float64 {
	if den == 0 {
		return 0
	}
	return round2(float64(num) / float64(den))
}

// round2 redondea a 2 decimales (presentación estable, determinista).
func round2(f float64) float64 {
	return math.Round(f*100) / 100
}
