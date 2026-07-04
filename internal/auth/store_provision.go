package auth

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"faro/internal/dberr"
)

// isUniqueViolation detecta el error de Postgres por violación de unicidad (23505).
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// createTenantWithOwner crea el negocio y su dueño en una transacción.
func (s *store) createTenantWithOwner(ctx context.Context, name, ownerEmail, ownerName, ownerHash string) (Tenant, User, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Tenant{}, User{}, err
	}
	defer tx.Rollback(ctx)

	var t Tenant
	if err := tx.QueryRow(ctx,
		`INSERT INTO tenants (name) VALUES ($1)
		 RETURNING id::text, name, status, created_at`, name).
		Scan(&t.ID, &t.Name, &t.Status, &t.CreatedAt); err != nil {
		return Tenant{}, User{}, err
	}

	var u User
	if err := tx.QueryRow(ctx,
		`INSERT INTO users (tenant_id, email, password_hash, name)
		 VALUES ($1, $2, $3, $4)
		 RETURNING id::text, tenant_id::text, email, name, is_super_admin, status, created_at`,
		t.ID, ownerEmail, ownerHash, ownerName).
		Scan(&u.ID, &u.TenantID, &u.Email, &u.Name, &u.IsSuperAdmin, &u.Status, &u.CreatedAt); err != nil {
		if isUniqueViolation(err) {
			return Tenant{}, User{}, ErrEmailTaken
		}
		return Tenant{}, User{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return Tenant{}, User{}, err
	}
	return t, u, nil
}

// createUser inserta el usuario y sus membresías (M:N) en una transacción.
func (s *store) createUser(ctx context.Context, tenantID, email, name, hash string, branchIDs []string) (User, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback(ctx)

	var u User
	err = tx.QueryRow(ctx,
		`INSERT INTO users (tenant_id, email, password_hash, name)
		 VALUES ($1, $2, $3, $4)
		 RETURNING id::text, tenant_id::text, email, name, is_super_admin, status, created_at`,
		tenantID, email, hash, name).
		Scan(&u.ID, &u.TenantID, &u.Email, &u.Name, &u.IsSuperAdmin, &u.Status, &u.CreatedAt)
	if isUniqueViolation(err) {
		return User{}, ErrEmailTaken
	}
	if err != nil {
		return User{}, err
	}
	if err := insertMemberships(ctx, tx, tenantID, u.ID, branchIDs); err != nil {
		return User{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return User{}, err
	}
	u.Branches, err = s.userBranches(ctx, u.ID)
	return u, err
}

// updateUser aplica el patch (name y/o membresías) a un usuario del negocio y
// devuelve el usuario actualizado con sus branches.
func (s *store) updateUser(ctx context.Context, tenantID, id string, patch UserPatch) (User, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback(ctx)

	tag, err := tx.Exec(ctx,
		`UPDATE users SET name = COALESCE($3, name) WHERE id = $1 AND tenant_id = $2`,
		id, tenantID, patch.Name)
	if dberr.IsInvalidText(err) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, err
	}
	if tag.RowsAffected() == 0 {
		return User{}, ErrNotFound
	}

	if patch.BranchIDs != nil {
		if _, err := tx.Exec(ctx, `DELETE FROM user_branches WHERE user_id = $1`, id); err != nil {
			return User{}, err
		}
		if err := insertMemberships(ctx, tx, tenantID, id, *patch.BranchIDs); err != nil {
			return User{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return User{}, err
	}

	u, err := s.userByID(ctx, id)
	if err != nil {
		return User{}, err
	}
	u.Branches, err = s.userBranches(ctx, id)
	return u, err
}

// insertMemberships escribe las filas de user_branches (idempotente).
func insertMemberships(ctx context.Context, tx pgx.Tx, tenantID, userID string, branchIDs []string) error {
	for _, bid := range branchIDs {
		if _, err := tx.Exec(ctx,
			`INSERT INTO user_branches (user_id, branch_id, tenant_id)
			 VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`, userID, bid, tenantID); err != nil {
			return err
		}
	}
	return nil
}

// countOwnedBranches cuenta cuántos de los ids son sucursales del negocio.
func (s *store) countOwnedBranches(ctx context.Context, tenantID string, ids []string) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx,
		`SELECT count(DISTINCT id) FROM branches WHERE tenant_id = $1 AND id = ANY($2::uuid[])`,
		tenantID, ids).Scan(&n)
	if dberr.IsInvalidText(err) {
		return 0, nil
	}
	return n, err
}

// listUsersByTenant lista usuarios del negocio con sus membresías (branches[]).
func (s *store) listUsersByTenant(ctx context.Context, tenantID string) ([]User, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id::text, tenant_id::text, email, name, is_super_admin, status, created_at
		   FROM users WHERE tenant_id = $1 ORDER BY created_at`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.TenantID, &u.Email, &u.Name, &u.IsSuperAdmin, &u.Status, &u.CreatedAt); err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Cargar membresías de cada usuario (N pequeñas consultas; conjuntos chicos).
	for i := range users {
		br, err := s.userBranches(ctx, users[i].ID)
		if err != nil {
			return nil, err
		}
		users[i].Branches = br
	}
	return users, nil
}
