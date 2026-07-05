package auth

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrInvalidCredentials se devuelve siempre que el login falla, sin distinguir
// si el email no existe o la contraseña es incorrecta (no filtrar información).
var ErrInvalidCredentials = errors.New("invalid credentials")

// ErrWeakPassword indica que la nueva contraseña no cumple el mínimo.
var ErrWeakPassword = errors.New("weak password")

// Service expone la lógica de autenticación del módulo.
type Service struct {
	store        *store
	tokens       *tokenManager
	cookieSecure bool
	loginLimiter *rateLimiter // por IP+email: frena fuerza bruta contra una cuenta
	ipLimiter    *rateLimiter // por IP: frena credential stuffing (muchos emails desde una IP)
}

// NewService construye el servicio de auth.
func NewService(pool *pgxpool.Pool, jwtSecret string, sessionTTL time.Duration, cookieSecure bool) *Service {
	return &Service{
		store:        newStore(pool),
		tokens:       newTokenManager(jwtSecret, sessionTTL),
		cookieSecure: cookieSecure,
		loginLimiter: newRateLimiter(5, time.Minute),  // T6: 5/min por IP+email
		ipLimiter:    newRateLimiter(20, time.Minute), // T6: 20/min por IP
	}
}

// authenticate valida credenciales y devuelve el usuario si son correctas.
func (svc *Service) authenticate(ctx context.Context, email, password string) (User, error) {
	u, hash, err := svc.store.userByEmail(ctx, email)
	if errors.Is(err, ErrNotFound) {
		return User{}, ErrInvalidCredentials
	}
	if err != nil {
		return User{}, err
	}
	if u.Status != "active" || !checkPassword(hash, password) {
		return User{}, ErrInvalidCredentials
	}
	return u, nil
}

// ChangePassword verifica la contraseña actual del usuario y establece una nueva
// (mínimo 8 caracteres). Disponible para cualquier usuario autenticado.
func (svc *Service) ChangePassword(ctx context.Context, userID, current, next string) error {
	hash, err := svc.store.passwordHashByID(ctx, userID)
	if err != nil {
		return err
	}
	if !checkPassword(hash, current) {
		return ErrInvalidCredentials
	}
	if len(next) < 8 {
		return ErrWeakPassword
	}
	newHash, err := hashPassword(next)
	if err != nil {
		return err
	}
	return svc.store.updatePassword(ctx, userID, newHash)
}

// SeedSuperAdmin crea el super admin global si no existe (idempotente). Devuelve
// true si lo creó. Pensado para correr al arranque desde variables de entorno.
func (svc *Service) SeedSuperAdmin(ctx context.Context, email, password string) (bool, error) {
	exists, err := svc.store.superAdminExists(ctx, email)
	if err != nil {
		return false, err
	}
	if exists {
		return false, nil
	}
	hash, err := hashPassword(password)
	if err != nil {
		return false, err
	}
	if _, err := svc.store.createSuperAdmin(ctx, email, "Super Admin", hash); err != nil {
		return false, err
	}
	return true, nil
}
