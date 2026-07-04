package auth

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// Routes devuelve el router del módulo auth (se monta en /auth).
func (svc *Service) Routes() http.Handler {
	r := chi.NewRouter()
	r.Post("/login", svc.handleLogin)
	r.Post("/logout", svc.handleLogout)
	r.With(svc.RequireSession).Get("/me", svc.handleMe)
	r.With(svc.RequireSession).Post("/select-branch", svc.handleSelectBranch)
	return r
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// sessionResponse es la forma de /auth/login y /auth/me (ADR-007 §D4).
type sessionResponse struct {
	User             User        `json:"user"`
	Tenant           *MeTenant   `json:"tenant"`
	Branches         []BranchRef `json:"branches"`
	ActiveBranchID   *string     `json:"activeBranchId"`
	MustSelectBranch bool        `json:"mustSelectBranch"`
}

func (svc *Service) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Email == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "validation_error", "Email y contraseña son requeridos")
		return
	}

	// T6: rate limiting anti fuerza bruta (por IP+email) y anti credential stuffing (por IP).
	ip := clientIP(r)
	if !svc.loginLimiter.allow(ip+"|"+req.Email) || !svc.ipLimiter.allow(ip) {
		writeError(w, http.StatusTooManyRequests, "rate_limited", "Demasiados intentos, espera un momento")
		return
	}

	u, err := svc.authenticate(r.Context(), req.Email, req.Password)
	if err != nil {
		// Mensaje genérico: no revelar si el email existe.
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "Email o contraseña incorrectos")
		return
	}

	branches, err := svc.store.userBranches(r.Context(), u.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "No se pudo iniciar sesión")
		return
	}
	// Sucursal activa: 1 membresía => se fija de una; >1 => debe seleccionarla luego.
	// El super admin no tiene sucursales.
	var active *string
	if len(branches) == 1 {
		active = &branches[0].ID
	}
	mustSelect := len(branches) > 1 && active == nil

	token, exp, err := svc.tokens.issue(Claims{UserID: u.ID, TenantID: u.TenantID, IsSuperAdmin: u.IsSuperAdmin, ActiveBranchID: active})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "No se pudo iniciar sesión")
		return
	}

	tenant, err := svc.tenantForUser(r.Context(), u)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "No se pudo iniciar sesión")
		return
	}

	svc.setSessionCookie(w, token, exp)
	writeJSON(w, http.StatusOK, sessionResponse{
		User: u, Tenant: tenant, Branches: branches, ActiveBranchID: active, MustSelectBranch: mustSelect,
	})
}

func (svc *Service) handleLogout(w http.ResponseWriter, _ *http.Request) {
	svc.clearSessionCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

func (svc *Service) handleMe(w http.ResponseWriter, r *http.Request) {
	u, ok := UserFromContext(r.Context())
	if !ok {
		unauthorized(w)
		return
	}
	branches, err := svc.store.userBranches(r.Context(), u.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "No se pudo cargar la sesión")
		return
	}
	active, _ := ActiveBranchFromContext(r.Context())
	mustSelect := len(branches) > 1 && active == nil

	tenant, err := svc.tenantForUser(r.Context(), u)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "No se pudo cargar el negocio")
		return
	}
	writeJSON(w, http.StatusOK, sessionResponse{
		User: u, Tenant: tenant, Branches: branches, ActiveBranchID: active, MustSelectBranch: mustSelect,
	})
}

type selectBranchRequest struct {
	BranchID string `json:"branchId"`
}

func (svc *Service) handleSelectBranch(w http.ResponseWriter, r *http.Request) {
	u, ok := UserFromContext(r.Context())
	if !ok {
		unauthorized(w)
		return
	}
	var req selectBranchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.BranchID == "" {
		writeError(w, http.StatusBadRequest, "validation_error", "branchId es requerido")
		return
	}

	member, err := svc.store.branchMembership(r.Context(), u.ID, req.BranchID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "No se pudo seleccionar la sucursal")
		return
	}
	if member == nil {
		// Distinguir "no existe" (404) de "existe pero no soy miembro" (403).
		tid := ""
		if u.TenantID != nil {
			tid = *u.TenantID
		} else if bt, err := svc.store.businessTenantID(r.Context()); err == nil {
			tid = bt
		}
		exists := false
		if tid != "" {
			exists, _ = svc.store.branchExistsInTenant(r.Context(), tid, req.BranchID)
		}
		if exists {
			writeError(w, http.StatusForbidden, "not_member", "No perteneces a esa sucursal")
		} else {
			writeError(w, http.StatusNotFound, "branch_not_found", "La sucursal no existe")
		}
		return
	}

	token, exp, err := svc.tokens.issue(Claims{UserID: u.ID, TenantID: u.TenantID, IsSuperAdmin: u.IsSuperAdmin, ActiveBranchID: &member.ID})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "No se pudo seleccionar la sucursal")
		return
	}
	svc.setSessionCookie(w, token, exp)
	writeJSON(w, http.StatusOK, map[string]any{"activeBranchId": member.ID, "branch": member})
}

// tenantForUser devuelve el negocio (para favicon/marca) del usuario, o nil si es
// super admin global (sin tenant).
func (svc *Service) tenantForUser(ctx context.Context, u User) (*MeTenant, error) {
	if u.TenantID == nil {
		return nil, nil
	}
	t, err := svc.store.tenantForMe(ctx, *u.TenantID)
	if err != nil {
		return nil, err
	}
	return &t, nil
}
