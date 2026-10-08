package dayclose

import (
	"context"
	"errors"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound     = errors.New("not found")
	ErrValidation   = errors.New("validation")
	ErrInvalidState = errors.New("invalid state")
)

type store struct {
	pool *pgxpool.Pool
}

func newStore(pool *pgxpool.Pool) *store {
	return &store{pool: pool}
}

func pgCode(err error, code string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == code
}

func isInvalidUUID(err error) bool { return pgCode(err, "22P02") }

// closureCols es la lista de columnas del SELECT de un cierre (con nombres resueltos).
const closureCols = `c.id::text, c.branch_id::text, b.name, c.closure_date::text, c.status,
	c.total_sales_cents, c.total_expenses_cents, c.cash_expected_cents, c.cash_counted_cents,
	c.cash_diff_cents, c.bakery_count_id::text, c.supply_requisition_id::text, c.notes,
	cu.name, su.name, c.submitted_at, c.created_at, c.updated_at`

const closureFrom = `FROM branch_day_closures c
	JOIN branches b ON b.id = c.branch_id
	LEFT JOIN users cu ON cu.id = c.created_by
	LEFT JOIN users su ON su.id = c.submitted_by`

func scanClosure(row pgx.Row) (Closure, error) {
	var c Closure
	err := row.Scan(&c.ID, &c.BranchID, &c.BranchName, &c.ClosureDate, &c.Status,
		&c.TotalSalesCents, &c.TotalExpensesCents, &c.CashExpectedCents, &c.CashCountedCents,
		&c.CashDiffCents, &c.BakeryCountID, &c.SupplyRequisitionID, &c.Notes,
		&c.CreatedByName, &c.SubmittedByName, &c.SubmittedAt, &c.CreatedAt, &c.UpdatedAt)
	return c, err
}

// getOrCreate es idempotente: si ya existe el cierre de (branch, date) lo devuelve; si no,
// lo crea en 'draft'. created indica si se creó en esta llamada.
func (s *store) getOrCreate(ctx context.Context, tenantID, branchID, date, createdBy string) (Closure, bool, error) {
	tag, err := s.pool.Exec(ctx,
		`INSERT INTO branch_day_closures (tenant_id, branch_id, closure_date, created_by)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (branch_id, closure_date) DO NOTHING`,
		tenantID, branchID, date, createdBy)
	if err != nil {
		return Closure{}, false, err
	}
	created := tag.RowsAffected() == 1

	c, err := scanClosure(s.pool.QueryRow(ctx,
		`SELECT `+closureCols+` `+closureFrom+`
		  WHERE c.branch_id = $1 AND c.closure_date = $2 AND c.tenant_id = $3`,
		branchID, date, tenantID))
	if err != nil {
		return Closure{}, false, err
	}
	return c, created, nil
}

// getByID devuelve un cierre del negocio. uuid/ajeno => ErrNotFound.
func (s *store) getByID(ctx context.Context, tenantID, id string) (Closure, error) {
	c, err := scanClosure(s.pool.QueryRow(ctx,
		`SELECT `+closureCols+` `+closureFrom+` WHERE c.id = $1 AND c.tenant_id = $2`, id, tenantID))
	switch {
	case errors.Is(err, pgx.ErrNoRows), isInvalidUUID(err):
		return Closure{}, ErrNotFound
	case err != nil:
		return Closure{}, err
	}
	return c, nil
}

// updateDraft aplica el patch parcial a un cierre en 'draft' (COALESCE: nil = no cambia).
// No-draft => ErrInvalidState; inexistente => ErrNotFound.
//
// TODO(v2): permitir limpiar campos explícitamente vía sentinela. Con COALESCE, enviar
// null en notes/bakeryCountId/supplyRequisitionId/cashCountedCents NO los limpia (se
// interpreta como "no cambia"); para el MVP solo se pueden fijar/actualizar valores.
func (s *store) updateDraft(ctx context.Context, tenantID, id string, p DraftPatch) (Closure, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Closure{}, err
	}
	defer tx.Rollback(ctx)

	var status string
	err = tx.QueryRow(ctx,
		`SELECT status FROM branch_day_closures WHERE id = $1 AND tenant_id = $2 FOR UPDATE`,
		id, tenantID).Scan(&status)
	switch {
	case errors.Is(err, pgx.ErrNoRows), isInvalidUUID(err):
		return Closure{}, ErrNotFound
	case err != nil:
		return Closure{}, err
	}
	if status != "draft" {
		return Closure{}, ErrInvalidState
	}
	if _, err := tx.Exec(ctx,
		`UPDATE branch_day_closures
		    SET cash_counted_cents      = COALESCE($3, cash_counted_cents),
		        notes                   = COALESCE($4, notes),
		        bakery_count_id         = COALESCE($5, bakery_count_id),
		        supply_requisition_id   = COALESCE($6, supply_requisition_id),
		        updated_at = now()
		  WHERE id = $1 AND tenant_id = $2`,
		id, tenantID, p.CashCountedCents, p.Notes, p.BakeryCountID, p.SupplyRequisitionID); err != nil {
		return Closure{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Closure{}, err
	}
	return s.getByID(ctx, tenantID, id)
}

// submit congela el snapshot (totales calculados en vivo) y pasa a 'submitted' bajo
// FOR UPDATE. Ya submitted => ErrInvalidState; inexistente => ErrNotFound.
func (s *store) submit(ctx context.Context, tenantID, id string, totalSales, totalExpenses, cashExpected int, submittedBy string) (Closure, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Closure{}, err
	}
	defer tx.Rollback(ctx)

	var status string
	var cashCounted *int
	err = tx.QueryRow(ctx,
		`SELECT status, cash_counted_cents FROM branch_day_closures
		  WHERE id = $1 AND tenant_id = $2 FOR UPDATE`,
		id, tenantID).Scan(&status, &cashCounted)
	switch {
	case errors.Is(err, pgx.ErrNoRows), isInvalidUUID(err):
		return Closure{}, ErrNotFound
	case err != nil:
		return Closure{}, err
	}
	if status != "draft" {
		return Closure{}, ErrInvalidState
	}
	// cash_diff = contado - esperado (solo si hay conteo de efectivo).
	var cashDiff *int
	if cashCounted != nil {
		d := *cashCounted - cashExpected
		cashDiff = &d
	}
	if _, err := tx.Exec(ctx,
		`UPDATE branch_day_closures
		    SET status = 'submitted',
		        total_sales_cents     = $3,
		        total_expenses_cents  = $4,
		        cash_expected_cents   = $5,
		        cash_diff_cents       = $6,
		        submitted_by          = $7,
		        submitted_at          = now(),
		        updated_at            = now()
		  WHERE id = $1 AND tenant_id = $2`,
		id, tenantID, totalSales, totalExpenses, cashExpected, cashDiff, submittedBy); err != nil {
		return Closure{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Closure{}, err
	}
	return s.getByID(ctx, tenantID, id)
}

// list lista cierres del negocio (DESC por fecha) con filtros opcionales de sucursal y rango
// de fechas (YYYY-MM-DD).
func (s *store) list(ctx context.Context, tenantID string, branchID, from, to *string) ([]Closure, error) {
	args := []any{tenantID}
	sql := `SELECT ` + closureCols + ` ` + closureFrom + ` WHERE c.tenant_id = $1`
	if branchID != nil {
		args = append(args, *branchID)
		sql += ` AND c.branch_id = $2`
	}
	if from != nil {
		args = append(args, *from)
		sql += ` AND c.closure_date >= $` + strconv.Itoa(len(args))
	}
	if to != nil {
		args = append(args, *to)
		sql += ` AND c.closure_date <= $` + strconv.Itoa(len(args))
	}
	sql += ` ORDER BY c.closure_date DESC, c.created_at DESC LIMIT 200`

	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		if isInvalidUUID(err) {
			return []Closure{}, nil
		}
		return nil, err
	}
	defer rows.Close()

	out := []Closure{}
	for rows.Next() {
		c, err := scanClosure(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// bakeryCountInBranch indica si el conteo pertenece al negocio y a la sucursal dada.
func (s *store) bakeryCountInBranch(ctx context.Context, tenantID, branchID, id string) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM bakery_counts WHERE id = $1 AND tenant_id = $2 AND branch_id = $3)`,
		id, tenantID, branchID).Scan(&ok)
	if isInvalidUUID(err) {
		return false, nil
	}
	return ok, err
}

// requisitionInBranch indica si la requisición pertenece al negocio y a la sucursal dada.
func (s *store) requisitionInBranch(ctx context.Context, tenantID, branchID, id string) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM supply_requisitions WHERE id = $1 AND tenant_id = $2 AND branch_id = $3)`,
		id, tenantID, branchID).Scan(&ok)
	if isInvalidUUID(err) {
		return false, nil
	}
	return ok, err
}
