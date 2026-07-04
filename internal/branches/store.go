package branches

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"faro/internal/dberr"
)

var (
	// ErrNotFound indica que la sucursal no existe (o es de otro negocio).
	ErrNotFound = errors.New("not found")
	// ErrNameTaken indica que ya existe una sucursal con ese nombre en el negocio.
	ErrNameTaken = errors.New("name taken")
	// ErrInUse indica que la sucursal está referenciada por usuarios o ventas.
	ErrInUse = errors.New("branch in use")
)

type store struct {
	pool *pgxpool.Pool
}

func newStore(pool *pgxpool.Pool) *store {
	return &store{pool: pool}
}

const branchCols = `id::text, tenant_id::text, name, status, created_at, updated_at`

func scanBranch(row pgx.Row) (Branch, error) {
	var b Branch
	err := row.Scan(&b.ID, &b.TenantID, &b.Name, &b.Status, &b.CreatedAt, &b.UpdatedAt)
	return b, err
}

// list devuelve las sucursales del negocio. status: "active" | "inactive" | "" (todas).
func (s *store) list(ctx context.Context, tenantID, status string) ([]Branch, error) {
	q := `SELECT ` + branchCols + ` FROM branches WHERE tenant_id = $1`
	args := []any{tenantID}
	if status != "" {
		q += ` AND status = $2`
		args = append(args, status)
	}
	q += ` ORDER BY name`

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Branch{}
	for rows.Next() {
		b, err := scanBranch(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func (s *store) get(ctx context.Context, tenantID, id string) (Branch, error) {
	b, err := scanBranch(s.pool.QueryRow(ctx,
		`SELECT `+branchCols+` FROM branches WHERE id = $1 AND tenant_id = $2`, id, tenantID))
	if errors.Is(err, pgx.ErrNoRows) || dberr.IsInvalidText(err) {
		return Branch{}, ErrNotFound
	}
	if err != nil {
		return Branch{}, err
	}
	return b, nil
}

func (s *store) create(ctx context.Context, tenantID, name string) (Branch, error) {
	b, err := scanBranch(s.pool.QueryRow(ctx,
		`INSERT INTO branches (tenant_id, name) VALUES ($1, $2) RETURNING `+branchCols,
		tenantID, name))
	if dberr.IsUniqueViolation(err) {
		return Branch{}, ErrNameTaken
	}
	if err != nil {
		return Branch{}, err
	}
	return b, nil
}

// update aplica el patch (name/status) con COALESCE: los nil no cambian el campo.
func (s *store) update(ctx context.Context, tenantID, id string, patch BranchPatch) (Branch, error) {
	b, err := scanBranch(s.pool.QueryRow(ctx,
		`UPDATE branches
		    SET name = COALESCE($3, name),
		        status = COALESCE($4, status),
		        updated_at = now()
		  WHERE id = $1 AND tenant_id = $2
		 RETURNING `+branchCols,
		id, tenantID, patch.Name, patch.Status))
	if errors.Is(err, pgx.ErrNoRows) || dberr.IsInvalidText(err) {
		return Branch{}, ErrNotFound
	}
	if dberr.IsUniqueViolation(err) {
		return Branch{}, ErrNameTaken
	}
	if err != nil {
		return Branch{}, err
	}
	return b, nil
}

// delete borra físicamente la sucursal. Se bloquea (ErrInUse) si hay membresías de
// usuario (user_branches) o ventas que la referencian: la membresía es ON DELETE
// CASCADE, por eso se comprueba explícitamente antes de borrar. Sin la fila (o de
// otro negocio) -> ErrNotFound.
func (s *store) delete(ctx context.Context, tenantID, id string) error {
	// Aislamiento + existencia: la sucursal debe ser del negocio.
	if _, err := s.get(ctx, tenantID, id); err != nil {
		return err
	}
	var inUse bool
	if err := s.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM user_branches WHERE branch_id = $1)
		     OR EXISTS(SELECT 1 FROM sales WHERE branch_id = $1)`, id).Scan(&inUse); err != nil {
		return err
	}
	if inUse {
		return ErrInUse
	}
	tag, err := s.pool.Exec(ctx, `DELETE FROM branches WHERE id = $1 AND tenant_id = $2`, id, tenantID)
	switch {
	case dberr.IsForeignKeyViolation(err):
		return ErrInUse
	case err != nil:
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
