package settings

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"faro/internal/dberr"
)

// ErrNotFound indica que el negocio no existe.
var ErrNotFound = errors.New("not found")

type store struct {
	pool *pgxpool.Pool
}

func newStore(pool *pgxpool.Pool) *store {
	return &store{pool: pool}
}

func (s *store) get(ctx context.Context, tenantID string) (Settings, error) {
	var st Settings
	err := s.pool.QueryRow(ctx,
		`SELECT id::text, name, favicon_url FROM tenants WHERE id = $1`, tenantID).
		Scan(&st.Tenant.ID, &st.Tenant.Name, &st.FaviconURL)
	if errors.Is(err, pgx.ErrNoRows) || dberr.IsInvalidText(err) {
		return Settings{}, ErrNotFound
	}
	if err != nil {
		return Settings{}, err
	}
	return st, nil
}

func (s *store) setFavicon(ctx context.Context, tenantID, url string) (string, error) {
	var out string
	err := s.pool.QueryRow(ctx,
		`UPDATE tenants SET favicon_url = $2 WHERE id = $1 RETURNING favicon_url`,
		tenantID, url).Scan(&out)
	if errors.Is(err, pgx.ErrNoRows) || dberr.IsInvalidText(err) {
		return "", ErrNotFound
	}
	return out, err
}

func (s *store) clearFavicon(ctx context.Context, tenantID string) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE tenants SET favicon_url = NULL WHERE id = $1`, tenantID)
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
