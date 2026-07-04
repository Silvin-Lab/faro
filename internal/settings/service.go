package settings

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrValidation indica un faviconUrl inválido (vacío o fuera de /files/*).
var ErrValidation = errors.New("validation")

type Service struct {
	store *store
}

func NewService(pool *pgxpool.Pool) *Service {
	return &Service{store: newStore(pool)}
}

// Get devuelve los ajustes del negocio (tenant + favicon).
func (svc *Service) Get(ctx context.Context, tenantID string) (Settings, error) {
	return svc.store.get(ctx, tenantID)
}

// SetFavicon persiste la URL del favicon. Solo se acepta una ruta relativa
// /files/* (subida previa vía /uploads); no un host arbitrario.
func (svc *Service) SetFavicon(ctx context.Context, tenantID, url string) (string, error) {
	url = strings.TrimSpace(url)
	if !validFaviconURL(url) {
		return "", ErrValidation
	}
	return svc.store.setFavicon(ctx, tenantID, url)
}

// ClearFavicon quita el favicon del negocio (favicon_url -> NULL).
func (svc *Service) ClearFavicon(ctx context.Context, tenantID string) error {
	return svc.store.clearFavicon(ctx, tenantID)
}

// validFaviconURL exige una ruta relativa que apunte a /files/ (no host externo).
func validFaviconURL(url string) bool {
	return strings.HasPrefix(url, "/files/") && len(url) > len("/files/")
}
