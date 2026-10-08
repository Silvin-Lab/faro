package agreementdiscounts

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"faro/internal/dberr"
)

var (
	ErrNotFound  = errors.New("not found")
	ErrDuplicate = errors.New("duplicate active percent")
)

type store struct {
	pool *pgxpool.Pool
}

func newStore(pool *pgxpool.Pool) *store {
	return &store{pool: pool}
}

// list devuelve los descuentos del negocio. status: "active" | "inactive" | "all".
// Orden por % ascendente (el POS muestra los botones en ese orden).
func (s *store) list(ctx context.Context, tenantID, status string) ([]AgreementDiscount, error) {
	q := `SELECT id::text, percent, status, created_at, updated_at
	        FROM agreement_discounts WHERE tenant_id = $1`
	args := []any{tenantID}
	if status == "active" || status == "inactive" {
		q += " AND status = $2"
		args = append(args, status)
	}
	q += " ORDER BY percent ASC"

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []AgreementDiscount{}
	for rows.Next() {
		var d AgreementDiscount
		if err := rows.Scan(&d.ID, &d.Percent, &d.Status, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *store) get(ctx context.Context, tenantID, id string) (AgreementDiscount, error) {
	var d AgreementDiscount
	err := s.pool.QueryRow(ctx,
		`SELECT id::text, percent, status, created_at, updated_at
		   FROM agreement_discounts WHERE id = $1 AND tenant_id = $2`, id, tenantID).
		Scan(&d.ID, &d.Percent, &d.Status, &d.CreatedAt, &d.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) || dberr.IsInvalidText(err) {
		return AgreementDiscount{}, ErrNotFound
	}
	if err != nil {
		return AgreementDiscount{}, err
	}
	return d, nil
}

// create inserta un descuento activo. Choca con el índice único parcial si ya hay
// un activo con el mismo % (ErrDuplicate).
func (s *store) create(ctx context.Context, tenantID string, percent int) (AgreementDiscount, error) {
	var d AgreementDiscount
	err := s.pool.QueryRow(ctx,
		`INSERT INTO agreement_discounts (tenant_id, percent)
		 VALUES ($1, $2)
		 RETURNING id::text, percent, status, created_at, updated_at`,
		tenantID, percent).
		Scan(&d.ID, &d.Percent, &d.Status, &d.CreatedAt, &d.UpdatedAt)
	if dberr.IsUniqueViolation(err) {
		return AgreementDiscount{}, ErrDuplicate
	}
	if err != nil {
		return AgreementDiscount{}, err
	}
	return d, nil
}

// update cambia el % de un descuento existente del negocio. Choca con el índice
// único parcial si el nuevo % colisiona con otro activo (ErrDuplicate).
func (s *store) update(ctx context.Context, tenantID, id string, percent int) (AgreementDiscount, error) {
	var d AgreementDiscount
	err := s.pool.QueryRow(ctx,
		`UPDATE agreement_discounts SET percent = $3, updated_at = now()
		  WHERE id = $1 AND tenant_id = $2
		 RETURNING id::text, percent, status, created_at, updated_at`,
		id, tenantID, percent).
		Scan(&d.ID, &d.Percent, &d.Status, &d.CreatedAt, &d.UpdatedAt)
	if dberr.IsUniqueViolation(err) {
		return AgreementDiscount{}, ErrDuplicate
	}
	if errors.Is(err, pgx.ErrNoRows) || dberr.IsInvalidText(err) {
		return AgreementDiscount{}, ErrNotFound
	}
	if err != nil {
		return AgreementDiscount{}, err
	}
	return d, nil
}

// archive baja un descuento (soft delete): status = 'inactive'.
func (s *store) archive(ctx context.Context, tenantID, id string) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE agreement_discounts SET status = 'inactive', updated_at = now()
		  WHERE id = $1 AND tenant_id = $2`, id, tenantID)
	if dberr.IsInvalidText(err) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
