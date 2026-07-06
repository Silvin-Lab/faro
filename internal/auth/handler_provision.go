package auth

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// requireSuperAdmin restringe el acceso al super admin global. Asume que
// RequireSession ya corrió (usuario en contexto).
func (svc *Service) requireSuperAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, ok := UserFromContext(r.Context())
		if !ok {
			unauthorized(w)
			return
		}
		if !u.IsSuperAdmin {
			writeError(w, http.StatusForbidden, "forbidden", "Requiere super admin global")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// UserRoutes: alta, edición y listado de usuarios (solo super admin, ADR-007 §D6).
// Se monta en /users. El super admin resuelve el negocio con businessTenantID().
func (svc *Service) UserRoutes() http.Handler {
	r := chi.NewRouter()
	r.Use(svc.RequireSuperAdmin)
	r.Post("/", svc.handleCreateUser)
	r.Get("/", svc.handleListUsers)
	r.Patch("/{id}", svc.handleUpdateUser)
	return r
}

type createUserRequest struct {
	Email     string   `json:"email"`
	Password  string   `json:"password"`
	Name      string   `json:"name"`
	Role      string   `json:"role"`      // super_admin | branch_admin | cashier | barista
	BranchIDs []string `json:"branchIds"` // 1+ sucursales del negocio (roles de sucursal)
}

func (svc *Service) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := ResolveTenant(w, r)
	if !ok {
		return
	}
	var req createUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", "Cuerpo inválido")
		return
	}
	u, err := svc.CreateUser(r.Context(), tenantID, req.Email, req.Password, req.Name, req.Role, req.BranchIDs)
	switch {
	case errors.Is(err, ErrValidation):
		writeError(w, http.StatusBadRequest, "validation_error", "Email, contraseña (≥ 8), rol válido y —salvo super_admin— al menos una sucursal son requeridos")
	case errors.Is(err, ErrBranchNotFound):
		writeError(w, http.StatusNotFound, "branch_not_found", "Alguna sucursal no existe o no pertenece al negocio")
	case errors.Is(err, ErrEmailTaken):
		writeError(w, http.StatusConflict, "email_taken", "El email ya está registrado")
	case err != nil:
		writeError(w, http.StatusInternalServerError, "internal", "No se pudo crear el usuario")
	default:
		writeJSON(w, http.StatusCreated, map[string]any{"user": u})
	}
}

func (svc *Service) handleUpdateUser(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := ResolveTenant(w, r)
	if !ok {
		return
	}
	// Decodificar a map para distinguir campos ausentes de presentes.
	var raw map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", "Cuerpo inválido")
		return
	}
	var patch UserPatch
	if v, ok := raw["name"]; ok {
		var name string
		if err := json.Unmarshal(v, &name); err != nil {
			writeError(w, http.StatusBadRequest, "validation_error", "name inválido")
			return
		}
		patch.Name = &name
	}
	if v, ok := raw["role"]; ok {
		var role string
		if err := json.Unmarshal(v, &role); err != nil {
			writeError(w, http.StatusBadRequest, "validation_error", "role inválido")
			return
		}
		patch.Role = &role
	}
	if v, ok := raw["branchIds"]; ok {
		var ids []string
		if err := json.Unmarshal(v, &ids); err != nil {
			writeError(w, http.StatusBadRequest, "validation_error", "branchIds inválido")
			return
		}
		patch.BranchIDs = &ids
	}

	u, err := svc.UpdateUser(r.Context(), tenantID, chi.URLParam(r, "id"), patch)
	switch {
	case errors.Is(err, ErrValidation):
		writeError(w, http.StatusBadRequest, "validation_error", "Datos inválidos (nombre o al menos una sucursal)")
	case errors.Is(err, ErrBranchNotFound):
		writeError(w, http.StatusNotFound, "branch_not_found", "Alguna sucursal no existe o no pertenece al negocio")
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "Usuario no encontrado")
	case err != nil:
		writeError(w, http.StatusInternalServerError, "internal", "No se pudo actualizar el usuario")
	default:
		writeJSON(w, http.StatusOK, map[string]any{"user": u})
	}
}

func (svc *Service) handleListUsers(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := ResolveTenant(w, r)
	if !ok {
		return
	}
	users, err := svc.ListUsers(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "No se pudieron listar los usuarios")
		return
	}
	if users == nil {
		users = []User{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": users})
}
