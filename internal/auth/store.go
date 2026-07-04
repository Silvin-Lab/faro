package auth

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"faro/internal/dberr"
)

// ErrNotFound indica que no existe el registro buscado.
var ErrNotFound = errors.New("not found")

// ErrNoBusiness / ErrMultipleBusiness señalan estados inválidos del negocio único
// (ADR-007 §D2): debe existir exactamente una fila en tenants.
var (
	ErrNoBusiness       = errors.New("no business tenant")
	ErrMultipleBusiness = errors.New("multiple business tenants")
)

type store struct {
	pool *pgxpool.Pool
}

func newStore(pool *pgxpool.Pool) *store {
	return &store{pool: pool}
}

// userByEmail devuelve el usuario y su password_hash (para verificar login).
func (s *store) userByEmail(ctx context.Context, email string) (User, string, error) {
	var u User
	var hash string
	err := s.pool.QueryRow(ctx,
		`SELECT id::text, tenant_id::text, email, password_hash, name, is_super_admin, status, created_at
		   FROM users WHERE email = $1`, email).
		Scan(&u.ID, &u.TenantID, &u.Email, &hash, &u.Name, &u.IsSuperAdmin, &u.Status, &u.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, "", ErrNotFound
	}
	if err != nil {
		return User{}, "", err
	}
	return u, hash, nil
}

// userByID carga un usuario por id (sin exponer el hash).
func (s *store) userByID(ctx context.Context, id string) (User, error) {
	var u User
	err := s.pool.QueryRow(ctx,
		`SELECT id::text, tenant_id::text, email, name, is_super_admin, status, created_at
		   FROM users WHERE id = $1`, id).
		Scan(&u.ID, &u.TenantID, &u.Email, &u.Name, &u.IsSuperAdmin, &u.Status, &u.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, err
	}
	return u, nil
}

// superAdminExists indica si ya existe un super admin global con ese email (para el seed idempotente).
func (s *store) superAdminExists(ctx context.Context, email string) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM users WHERE email = $1 AND is_super_admin = true)`, email).
		Scan(&exists)
	return exists, err
}

func (s *store) createSuperAdmin(ctx context.Context, email, name, hash string) (User, error) {
	var u User
	err := s.pool.QueryRow(ctx,
		`INSERT INTO users (tenant_id, email, password_hash, name, is_super_admin)
		 VALUES (NULL, $1, $2, $3, true)
		 RETURNING id::text, tenant_id::text, email, name, is_super_admin, status, created_at`,
		email, hash, name).
		Scan(&u.ID, &u.TenantID, &u.Email, &u.Name, &u.IsSuperAdmin, &u.Status, &u.CreatedAt)
	return u, err
}

// businessTenantID resuelve el negocio único (ADR-007 §D2). Falla ruidoso si hay
// 0 o >1 tenants (estado inválido para negocio único).
func (s *store) businessTenantID(ctx context.Context) (string, error) {
	rows, err := s.pool.Query(ctx, `SELECT id::text FROM tenants ORDER BY created_at LIMIT 2`)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return "", err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	switch len(ids) {
	case 0:
		return "", ErrNoBusiness
	case 1:
		return ids[0], nil
	default:
		return "", ErrMultipleBusiness
	}
}

// userBranches devuelve las membresías (sucursales) de un usuario, ordenadas por
// nombre. Vacío para el super admin.
func (s *store) userBranches(ctx context.Context, userID string) ([]BranchRef, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT b.id::text, b.name
		   FROM user_branches ub
		   JOIN branches b ON b.id = ub.branch_id
		  WHERE ub.user_id = $1
		  ORDER BY b.name`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []BranchRef{}
	for rows.Next() {
		var b BranchRef
		if err := rows.Scan(&b.ID, &b.Name); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// branchMembership devuelve la sucursal (id, name) si el usuario es miembro; nil si
// no lo es. err distingue fallos reales de "no miembro".
func (s *store) branchMembership(ctx context.Context, userID, branchID string) (*BranchRef, error) {
	var b BranchRef
	err := s.pool.QueryRow(ctx,
		`SELECT b.id::text, b.name
		   FROM user_branches ub
		   JOIN branches b ON b.id = ub.branch_id
		  WHERE ub.user_id = $1 AND ub.branch_id = $2`, userID, branchID).
		Scan(&b.ID, &b.Name)
	if errors.Is(err, pgx.ErrNoRows) || dberr.IsInvalidText(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &b, nil
}

// branchExistsInTenant indica si la sucursal existe y pertenece al negocio.
func (s *store) branchExistsInTenant(ctx context.Context, tenantID, branchID string) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM branches WHERE id = $1 AND tenant_id = $2)`, branchID, tenantID).
		Scan(&exists)
	if dberr.IsInvalidText(err) {
		return false, nil
	}
	return exists, err
}

// tenantForMe carga los datos del negocio expuestos en GET /auth/me.
func (s *store) tenantForMe(ctx context.Context, tenantID string) (MeTenant, error) {
	var t MeTenant
	err := s.pool.QueryRow(ctx,
		`SELECT id::text, name, favicon_url FROM tenants WHERE id = $1`, tenantID).
		Scan(&t.ID, &t.Name, &t.FaviconURL)
	if errors.Is(err, pgx.ErrNoRows) {
		return MeTenant{}, ErrNotFound
	}
	return t, err
}
