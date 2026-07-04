package auth

import (
	"context"
	"net/http"
)

type ctxKey int

const (
	userCtxKey ctxKey = iota
	activeBranchCtxKey
	businessTenantCtxKey
)

// RequireSession exige una sesión válida: lee la cookie, valida el JWT y carga
// al usuario (que debe seguir activo), dejándolo en el contexto de la petición.
// Además deja disponibles la sucursal activa del claim y —para el super admin— el
// tenant del negocio (businessTenantID) para ResolveTenant.
func (svc *Service) RequireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(sessionCookieName)
		if err != nil {
			unauthorized(w)
			return
		}
		claims, err := svc.tokens.parse(c.Value)
		if err != nil {
			unauthorized(w)
			return
		}
		u, err := svc.store.userByID(r.Context(), claims.UserID)
		if err != nil || u.Status != "active" {
			unauthorized(w)
			return
		}
		ctx := context.WithValue(r.Context(), userCtxKey, u)
		ctx = context.WithValue(ctx, activeBranchCtxKey, claims.ActiveBranchID)
		// El super admin (tenant_id NULL) resuelve el negocio con businessTenantID();
		// se cachea en el contexto (best-effort: si aún no hay negocio, se omite).
		if u.TenantID == nil {
			if bt, err := svc.store.businessTenantID(r.Context()); err == nil {
				ctx = context.WithValue(ctx, businessTenantCtxKey, bt)
			}
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequireSuperAdmin exige sesión válida y que el usuario sea super admin global.
func (svc *Service) RequireSuperAdmin(next http.Handler) http.Handler {
	return svc.RequireSession(svc.requireSuperAdmin(next))
}

// UserFromContext recupera el usuario autenticado puesto por RequireSession.
func UserFromContext(ctx context.Context) (User, bool) {
	u, ok := ctx.Value(userCtxKey).(User)
	return u, ok
}

// ActiveBranchFromContext devuelve la sucursal activa de la sesión (claim del JWT).
// El segundo valor es true si había sesión (el puntero puede ser nil).
func ActiveBranchFromContext(ctx context.Context) (*string, bool) {
	v, ok := ctx.Value(activeBranchCtxKey).(*string)
	return v, ok
}

func businessTenantFromContext(ctx context.Context) (string, bool) {
	v, ok := ctx.Value(businessTenantCtxKey).(string)
	return v, ok
}

// ResolveTenant devuelve el tenant del negocio para el caller: su propio tenant si
// lo tiene, o el del negocio único (businessTenantID) si es super admin. Escribe el
// error HTTP y devuelve false si no puede resolverse.
func ResolveTenant(w http.ResponseWriter, r *http.Request) (string, bool) {
	u, ok := UserFromContext(r.Context())
	if !ok {
		unauthorized(w)
		return "", false
	}
	if u.TenantID != nil {
		return *u.TenantID, true
	}
	if bt, ok := businessTenantFromContext(r.Context()); ok {
		return bt, true
	}
	writeError(w, http.StatusBadRequest, "tenant_required", "No hay un negocio configurado")
	return "", false
}

// TenantOf devuelve el tenant del usuario en sesión; el super admin global (sin
// negocio) no opera estos módulos -> 400. Úsalo en customers/sales (solo personal
// de sucursal).
func TenantOf(w http.ResponseWriter, r *http.Request) (string, bool) {
	u, ok := UserFromContext(r.Context())
	if !ok {
		unauthorized(w)
		return "", false
	}
	if u.TenantID == nil {
		writeError(w, http.StatusBadRequest, "tenant_required", "Esta operación requiere un negocio")
		return "", false
	}
	return *u.TenantID, true
}
