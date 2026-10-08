package requisitions

import (
	"context"
	"errors"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound      = errors.New("not found")
	ErrValidation    = errors.New("validation")
	ErrInvalidBranch = errors.New("invalid branch")
	ErrInvalidSupply = errors.New("invalid supply")
	ErrInvalidState  = errors.New("invalid state")
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

// isInvalidUUID detecta uuid mal formado (22P02): se trata como inexistente.
func isInvalidUUID(err error) bool { return pgCode(err, "22P02") }

// branchInTenant indica si la sucursal es del negocio.
func (s *store) branchInTenant(ctx context.Context, tenantID, branchID string) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM branches WHERE id = $1 AND tenant_id = $2)`,
		branchID, tenantID).Scan(&ok)
	if isInvalidUUID(err) {
		return false, nil
	}
	return ok, err
}

// supplyInTenant indica si el insumo es del negocio (uuid mal formado/ajeno => false).
func (s *store) supplyInTenant(ctx context.Context, tenantID, supplyID string) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM supplies WHERE id = $1 AND tenant_id = $2)`,
		supplyID, tenantID).Scan(&ok)
	if isInvalidUUID(err) {
		return false, nil
	}
	return ok, err
}

// ---- Crear ------------------------------------------------------------------

// createRequisition inserta el header (status='pending') y sus líneas en UNA transacción.
// Las líneas vienen ya validadas (insumos del negocio, cantidades > 0). Devuelve el detalle
// completo (header + líneas + movimientos, estos vacíos al crear).
func (s *store) createRequisition(ctx context.Context, tenantID, branchID string, note *string, lines []LineInput, requestedBy string) (RequisitionDetail, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return RequisitionDetail{}, err
	}
	defer tx.Rollback(ctx)

	var id string
	err = tx.QueryRow(ctx,
		`INSERT INTO supply_requisitions (tenant_id, branch_id, note, requested_by)
		 VALUES ($1, $2, $3, $4)
		 RETURNING id::text`,
		tenantID, branchID, note, requestedBy).Scan(&id)
	if err != nil {
		return RequisitionDetail{}, err
	}

	for _, ln := range lines {
		if _, err := tx.Exec(ctx,
			`INSERT INTO supply_requisition_items
			     (tenant_id, requisition_id, supply_id, quantity_requested, note)
			 VALUES ($1, $2, $3, $4, $5)`,
			tenantID, id, ln.SupplyID, ln.QuantityBase, ln.Note); err != nil {
			// Duplicado de insumo en la misma requisición => validación.
			if pgCode(err, "23505") {
				return RequisitionDetail{}, ErrValidation
			}
			return RequisitionDetail{}, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return RequisitionDetail{}, err
	}
	return s.getRequisition(ctx, tenantID, id)
}

// ---- Detalle ----------------------------------------------------------------

// getRequisition devuelve el header + líneas + movimientos ligados. uuid/ajeno =>
// ErrNotFound.
func (s *store) getRequisition(ctx context.Context, tenantID, id string) (RequisitionDetail, error) {
	var d RequisitionDetail
	err := s.pool.QueryRow(ctx,
		`SELECT r.id::text, r.branch_id::text, b.name, r.status, r.note, u.name, r.created_at, r.updated_at
		   FROM supply_requisitions r
		   JOIN branches b ON b.id = r.branch_id
		   LEFT JOIN users u ON u.id = r.requested_by
		  WHERE r.id = $1 AND r.tenant_id = $2`, id, tenantID).
		Scan(&d.Requisition.ID, &d.Requisition.BranchID, &d.Requisition.BranchName, &d.Requisition.Status,
			&d.Requisition.Note, &d.Requisition.RequestedByName, &d.Requisition.CreatedAt, &d.Requisition.UpdatedAt)
	switch {
	case errors.Is(err, pgx.ErrNoRows), isInvalidUUID(err):
		return RequisitionDetail{}, ErrNotFound
	case err != nil:
		return RequisitionDetail{}, err
	}

	itemRows, err := s.pool.Query(ctx,
		`SELECT i.id::text, i.supply_id::text, sp.name, sp.base_unit, i.quantity_requested,
		        i.quantity_fulfilled, i.note
		   FROM supply_requisition_items i
		   JOIN supplies sp ON sp.id = i.supply_id
		  WHERE i.requisition_id = $1 AND i.tenant_id = $2
		  ORDER BY sp.name`, id, tenantID)
	if err != nil {
		return RequisitionDetail{}, err
	}
	defer itemRows.Close()
	d.Items = []RequisitionItem{}
	for itemRows.Next() {
		var it RequisitionItem
		if err := itemRows.Scan(&it.ID, &it.SupplyID, &it.SupplyName, &it.BaseUnit,
			&it.QuantityRequested, &it.QuantityFulfilled, &it.Note); err != nil {
			return RequisitionDetail{}, err
		}
		d.Items = append(d.Items, it)
	}
	if err := itemRows.Err(); err != nil {
		return RequisitionDetail{}, err
	}

	movRows, err := s.pool.Query(ctx,
		`SELECT m.id::text, m.supply_id::text, sp.name, m.quantity_base, u.name, m.created_at
		   FROM warehouse_movements m
		   JOIN supplies sp ON sp.id = m.supply_id
		   LEFT JOIN users u ON u.id = m.created_by
		  WHERE m.requisition_id = $1 AND m.tenant_id = $2
		  ORDER BY m.created_at ASC`, id, tenantID)
	if err != nil {
		return RequisitionDetail{}, err
	}
	defer movRows.Close()
	d.Movements = []Movement{}
	for movRows.Next() {
		var mv Movement
		if err := movRows.Scan(&mv.ID, &mv.SupplyID, &mv.SupplyName, &mv.QuantityBase, &mv.CreatedByName, &mv.CreatedAt); err != nil {
			return RequisitionDetail{}, err
		}
		d.Movements = append(d.Movements, mv)
	}
	return d, movRows.Err()
}

// ---- Listado ----------------------------------------------------------------

// listRequisitions lista encabezados del negocio en orden FIFO (created_at ASC). branchID
// fuerza el scope de sucursal (nil = todas); status filtra por estado; q filtra por nombre
// de sucursal (ILIKE).
func (s *store) listRequisitions(ctx context.Context, tenantID string, branchID, status *string, q string) ([]Requisition, error) {
	args := []any{tenantID}
	sql := `SELECT r.id::text, r.branch_id::text, b.name, r.status, r.note, u.name, r.created_at, r.updated_at
		      FROM supply_requisitions r
		      JOIN branches b ON b.id = r.branch_id
		      LEFT JOIN users u ON u.id = r.requested_by
		     WHERE r.tenant_id = $1`
	if branchID != nil {
		args = append(args, *branchID)
		sql += ` AND r.branch_id = $` + strconv.Itoa(len(args))
	}
	if status != nil {
		args = append(args, *status)
		sql += ` AND r.status = $` + strconv.Itoa(len(args))
	}
	if q != "" {
		args = append(args, "%"+q+"%")
		sql += ` AND b.name ILIKE $` + strconv.Itoa(len(args))
	}
	sql += ` ORDER BY r.created_at ASC LIMIT 200`

	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		if isInvalidUUID(err) {
			return []Requisition{}, nil
		}
		return nil, err
	}
	defer rows.Close()

	out := []Requisition{}
	for rows.Next() {
		var r Requisition
		if err := rows.Scan(&r.ID, &r.BranchID, &r.BranchName, &r.Status, &r.Note, &r.RequestedByName, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ---- Cancelar / cerrar ------------------------------------------------------

// cancelRequisition cancela bajo FOR UPDATE: solo si status='pending' y ninguna línea tiene
// quantity_fulfilled>0. Cualquier otro caso => ErrInvalidState. Devuelve el header dueño
// para el gating del handler (branchID) aun cuando no pueda cancelarse.
func (s *store) cancelRequisition(ctx context.Context, tenantID, id string) (RequisitionDetail, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return RequisitionDetail{}, err
	}
	defer tx.Rollback(ctx)

	var status string
	err = tx.QueryRow(ctx,
		`SELECT status FROM supply_requisitions WHERE id = $1 AND tenant_id = $2 FOR UPDATE`,
		id, tenantID).Scan(&status)
	switch {
	case errors.Is(err, pgx.ErrNoRows), isInvalidUUID(err):
		return RequisitionDetail{}, ErrNotFound
	case err != nil:
		return RequisitionDetail{}, err
	}
	if status != "pending" {
		return RequisitionDetail{}, ErrInvalidState
	}
	var anyFulfilled bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM supply_requisition_items
		    WHERE requisition_id = $1 AND quantity_fulfilled > 0)`, id).Scan(&anyFulfilled); err != nil {
		return RequisitionDetail{}, err
	}
	if anyFulfilled {
		return RequisitionDetail{}, ErrInvalidState
	}
	if _, err := tx.Exec(ctx,
		`UPDATE supply_requisitions SET status = 'cancelled', updated_at = now()
		  WHERE id = $1 AND tenant_id = $2`, id, tenantID); err != nil {
		return RequisitionDetail{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return RequisitionDetail{}, err
	}
	return s.getRequisition(ctx, tenantID, id)
}

// closeRequisition fuerza partial->fulfilled bajo FOR UPDATE (cierre manual del
// super_admin). Solo desde 'partial'; otro estado => ErrInvalidState.
func (s *store) closeRequisition(ctx context.Context, tenantID, id string) (RequisitionDetail, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return RequisitionDetail{}, err
	}
	defer tx.Rollback(ctx)

	var status string
	err = tx.QueryRow(ctx,
		`SELECT status FROM supply_requisitions WHERE id = $1 AND tenant_id = $2 FOR UPDATE`,
		id, tenantID).Scan(&status)
	switch {
	case errors.Is(err, pgx.ErrNoRows), isInvalidUUID(err):
		return RequisitionDetail{}, ErrNotFound
	case err != nil:
		return RequisitionDetail{}, err
	}
	if status != "partial" {
		return RequisitionDetail{}, ErrInvalidState
	}
	if _, err := tx.Exec(ctx,
		`UPDATE supply_requisitions SET status = 'fulfilled', updated_at = now()
		  WHERE id = $1 AND tenant_id = $2`, id, tenantID); err != nil {
		return RequisitionDetail{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return RequisitionDetail{}, err
	}
	return s.getRequisition(ctx, tenantID, id)
}

// requisitionBranch devuelve la sucursal dueña de una requisición (para el gating de
// cancelación). uuid/ajeno => ErrNotFound.
func (s *store) requisitionBranch(ctx context.Context, tenantID, id string) (string, error) {
	var branchID string
	err := s.pool.QueryRow(ctx,
		`SELECT branch_id::text FROM supply_requisitions WHERE id = $1 AND tenant_id = $2`,
		id, tenantID).Scan(&branchID)
	switch {
	case errors.Is(err, pgx.ErrNoRows), isInvalidUUID(err):
		return "", ErrNotFound
	case err != nil:
		return "", err
	}
	return branchID, nil
}

// ---- Sugerencias ------------------------------------------------------------

// suggestions devuelve los insumos de una sucursal que están en o bajo su mínimo, con la
// cantidad sugerida = COALESCE(max, min*2) - stock. Puramente informativo.
func (s *store) suggestions(ctx context.Context, tenantID, branchID string) ([]Suggestion, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT sp.id::text, sp.name, sp.base_unit, sbs.stock_base, sbs.min_quantity, sbs.max_quantity
		   FROM supply_branch_stock sbs
		   JOIN supplies sp ON sp.id = sbs.supply_id
		  WHERE sbs.tenant_id = $1 AND sbs.branch_id = $2
		    AND sbs.min_quantity IS NOT NULL
		    AND sbs.stock_base <= sbs.min_quantity
		  ORDER BY sp.name`, tenantID, branchID)
	if err != nil {
		if isInvalidUUID(err) {
			return []Suggestion{}, nil
		}
		return nil, err
	}
	defer rows.Close()

	out := []Suggestion{}
	for rows.Next() {
		var sg Suggestion
		if err := rows.Scan(&sg.SupplyID, &sg.SupplyName, &sg.BaseUnit, &sg.StockBase, &sg.MinQuantity, &sg.MaxQuantity); err != nil {
			return nil, err
		}
		target := sg.MinQuantity * 2
		if sg.MaxQuantity != nil {
			target = *sg.MaxQuantity
		}
		sg.SuggestedQty = target - sg.StockBase
		if sg.SuggestedQty < 0 {
			sg.SuggestedQty = 0
		}
		out = append(out, sg)
	}
	return out, rows.Err()
}
