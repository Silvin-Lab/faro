package reports

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type store struct {
	pool *pgxpool.Pool
}

func newStore(pool *pgxpool.Pool) *store {
	return &store{pool: pool}
}

// branchClause devuelve el fragmento SQL y los args extra para el filtro por
// sucursal. alias es el prefijo de la columna (p. ej. "" o "s."). nextIdx es el
// índice del próximo placeholder posicional disponible.
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

// salesReport agrega las ventas del negocio en [from, to). tzMinutes es el offset
// del cliente (Date.getTimezoneOffset()) para calcular la hora local. branch acota
// opcionalmente el reporte a una sucursal (o al bucket "Sin sucursal").
func (s *store) salesReport(ctx context.Context, tenantID string, from, to time.Time, tzMinutes int, branch BranchFilter) (SalesReport, error) {
	rep := SalesReport{
		ByPaymentMethod: []PaymentBreakdown{},
		ByCategory:      []CategoryBreakdown{},
		ByHour:          []HourBreakdown{},
		ByBranch:        []BranchBreakdown{},
	}

	// Resumen.
	sumCond, sumArgs := branchClause("", 4, branch)
	args := append([]any{tenantID, from, to}, sumArgs...)
	if err := s.pool.QueryRow(ctx,
		`SELECT COUNT(*), COALESCE(SUM(total_cents), 0)
		   FROM sales WHERE tenant_id = $1 AND created_at >= $2 AND created_at < $3`+sumCond,
		args...).Scan(&rep.SalesCount, &rep.TotalCents); err != nil {
		return SalesReport{}, err
	}

	// Por forma de pago.
	pmCond, pmArgs := branchClause("", 4, branch)
	pmRows, err := s.pool.Query(ctx,
		`SELECT payment_method, COUNT(*), COALESCE(SUM(total_cents), 0)
		   FROM sales WHERE tenant_id = $1 AND created_at >= $2 AND created_at < $3`+pmCond+`
		  GROUP BY payment_method ORDER BY payment_method`,
		append([]any{tenantID, from, to}, pmArgs...)...)
	if err != nil {
		return SalesReport{}, err
	}
	for pmRows.Next() {
		var b PaymentBreakdown
		if err := pmRows.Scan(&b.Method, &b.Count, &b.TotalCents); err != nil {
			pmRows.Close()
			return SalesReport{}, err
		}
		rep.ByPaymentMethod = append(rep.ByPaymentMethod, b)
	}
	pmRows.Close()
	if err := pmRows.Err(); err != nil {
		return SalesReport{}, err
	}

	// Por categoría (line items -> producto -> categoría).
	catCond, catArgs := branchClause("s.", 4, branch)
	catRows, err := s.pool.Query(ctx,
		`SELECT COALESCE(c.name, 'Sin categoría'), COALESCE(SUM(si.quantity), 0), COALESCE(SUM(si.line_total_cents), 0)
		   FROM sale_items si
		   JOIN sales s ON s.id = si.sale_id
		   LEFT JOIN products p ON p.id = si.product_id
		   LEFT JOIN categories c ON c.id = p.category_id
		  WHERE s.tenant_id = $1 AND s.created_at >= $2 AND s.created_at < $3`+catCond+`
		  GROUP BY COALESCE(c.name, 'Sin categoría')
		  ORDER BY SUM(si.line_total_cents) DESC`,
		append([]any{tenantID, from, to}, catArgs...)...)
	if err != nil {
		return SalesReport{}, err
	}
	for catRows.Next() {
		var b CategoryBreakdown
		if err := catRows.Scan(&b.CategoryName, &b.Quantity, &b.TotalCents); err != nil {
			catRows.Close()
			return SalesReport{}, err
		}
		rep.ByCategory = append(rep.ByCategory, b)
	}
	catRows.Close()
	if err := catRows.Err(); err != nil {
		return SalesReport{}, err
	}

	// Por hora (hora local del cliente). El offset tz ocupa $4, el filtro va en $5.
	hrCond, hrArgs := branchClause("", 5, branch)
	hrRows, err := s.pool.Query(ctx,
		`SELECT EXTRACT(HOUR FROM (created_at - make_interval(mins => $4)))::int AS hr,
		        COUNT(*), COALESCE(SUM(total_cents), 0)
		   FROM sales WHERE tenant_id = $1 AND created_at >= $2 AND created_at < $3`+hrCond+`
		  GROUP BY hr ORDER BY hr`,
		append([]any{tenantID, from, to, tzMinutes}, hrArgs...)...)
	if err != nil {
		return SalesReport{}, err
	}
	for hrRows.Next() {
		var b HourBreakdown
		if err := hrRows.Scan(&b.Hour, &b.Count, &b.TotalCents); err != nil {
			hrRows.Close()
			return SalesReport{}, err
		}
		rep.ByHour = append(rep.ByHour, b)
	}
	hrRows.Close()
	if err := hrRows.Err(); err != nil {
		return SalesReport{}, err
	}

	// Por sucursal (desglose). El bucket NULL se etiqueta "Sin sucursal".
	brCond, brArgs := branchClause("s.", 4, branch)
	brRows, err := s.pool.Query(ctx,
		`SELECT s.branch_id::text, b.name, COALESCE(SUM(s.total_cents), 0), COUNT(*)
		   FROM sales s
		   LEFT JOIN branches b ON b.id = s.branch_id
		  WHERE s.tenant_id = $1 AND s.created_at >= $2 AND s.created_at < $3`+brCond+`
		  GROUP BY s.branch_id, b.name
		  ORDER BY SUM(s.total_cents) DESC`,
		append([]any{tenantID, from, to}, brArgs...)...)
	if err != nil {
		return SalesReport{}, err
	}
	for brRows.Next() {
		var b BranchBreakdown
		var name *string
		if err := brRows.Scan(&b.BranchID, &name, &b.TotalCents, &b.SalesCount); err != nil {
			brRows.Close()
			return SalesReport{}, err
		}
		if b.BranchID == nil || name == nil {
			b.BranchName = "Sin sucursal"
		} else {
			b.BranchName = *name
		}
		rep.ByBranch = append(rep.ByBranch, b)
	}
	brRows.Close()
	return rep, brRows.Err()
}

// expensesReport agrega los gastos del negocio en [from, to). branch acota
// opcionalmente a una sucursal. Reutiliza branchClause (mismo contrato que ventas).
func (s *store) expensesReport(ctx context.Context, tenantID string, from, to time.Time, branch BranchFilter) (ExpensesReport, error) {
	rep := ExpensesReport{
		ByCategory: []ExpenseCategoryBreakdown{},
		ByBranch:   []ExpenseBranchBreakdown{},
	}

	// Resumen.
	sumCond, sumArgs := branchClause("e.", 4, branch)
	if err := s.pool.QueryRow(ctx,
		`SELECT COUNT(*), COALESCE(SUM(e.amount_cents), 0)
		   FROM expenses e
		  WHERE e.tenant_id = $1 AND e.created_at >= $2 AND e.created_at < $3`+sumCond,
		append([]any{tenantID, from, to}, sumArgs...)...).
		Scan(&rep.Summary.ExpensesCount, &rep.Summary.TotalCents); err != nil {
		return ExpensesReport{}, err
	}

	// Por categoría de gasto (gasto -> concepto -> categoría). Bucket "Sin categoría".
	catCond, catArgs := branchClause("e.", 4, branch)
	catRows, err := s.pool.Query(ctx,
		`SELECT COALESCE(cat.name, 'Sin categoría'), COUNT(*), COALESCE(SUM(e.amount_cents), 0)
		   FROM expenses e
		   LEFT JOIN expense_concepts ec ON ec.id = e.concept_id
		   LEFT JOIN expense_categories cat ON cat.id = ec.category_id
		  WHERE e.tenant_id = $1 AND e.created_at >= $2 AND e.created_at < $3`+catCond+`
		  GROUP BY COALESCE(cat.name, 'Sin categoría')
		  ORDER BY SUM(e.amount_cents) DESC`,
		append([]any{tenantID, from, to}, catArgs...)...)
	if err != nil {
		return ExpensesReport{}, err
	}
	for catRows.Next() {
		var b ExpenseCategoryBreakdown
		if err := catRows.Scan(&b.CategoryName, &b.Count, &b.TotalCents); err != nil {
			catRows.Close()
			return ExpensesReport{}, err
		}
		rep.ByCategory = append(rep.ByCategory, b)
	}
	catRows.Close()
	if err := catRows.Err(); err != nil {
		return ExpensesReport{}, err
	}

	// Por sucursal (expenses.branch_id es NOT NULL; LEFT JOIN por robustez).
	brCond, brArgs := branchClause("e.", 4, branch)
	brRows, err := s.pool.Query(ctx,
		`SELECT e.branch_id::text, b.name, COUNT(*), COALESCE(SUM(e.amount_cents), 0)
		   FROM expenses e
		   LEFT JOIN branches b ON b.id = e.branch_id
		  WHERE e.tenant_id = $1 AND e.created_at >= $2 AND e.created_at < $3`+brCond+`
		  GROUP BY e.branch_id, b.name
		  ORDER BY SUM(e.amount_cents) DESC`,
		append([]any{tenantID, from, to}, brArgs...)...)
	if err != nil {
		return ExpensesReport{}, err
	}
	for brRows.Next() {
		var b ExpenseBranchBreakdown
		var name *string
		if err := brRows.Scan(&b.BranchID, &name, &b.Count, &b.TotalCents); err != nil {
			brRows.Close()
			return ExpensesReport{}, err
		}
		if b.BranchID == nil || name == nil {
			b.BranchName = "Sin sucursal"
		} else {
			b.BranchName = *name
		}
		rep.ByBranch = append(rep.ByBranch, b)
	}
	brRows.Close()
	return rep, brRows.Err()
}
