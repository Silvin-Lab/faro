package branches

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"faro/internal/auth"
)

// Routes se monta en /branches. Solo super admin (matriz §7): se monta con
// requireSuperAdmin. El tenant se resuelve con ResolveTenant (businessTenantID).
func (svc *Service) Routes(requireSuperAdmin func(http.Handler) http.Handler) http.Handler {
	r := chi.NewRouter()
	r.Use(requireSuperAdmin)
	r.Get("/", svc.handleList)
	r.Post("/", svc.handleCreate)
	r.Patch("/{id}", svc.handleUpdate)
	r.Delete("/{id}", svc.handleDelete)
	return r
}

func tenantOf(w http.ResponseWriter, r *http.Request) (string, bool) {
	return auth.ResolveTenant(w, r)
}

func (svc *Service) handleList(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := tenantOf(w, r)
	if !ok {
		return
	}
	items, err := svc.List(r.Context(), tenantID, r.URL.Query().Get("status"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "No se pudieron listar las sucursales")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

type createRequest struct {
	Name string `json:"name"`
}

func (svc *Service) handleCreate(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := tenantOf(w, r)
	if !ok {
		return
	}
	var req createRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", "Cuerpo inválido")
		return
	}
	b, err := svc.Create(r.Context(), tenantID, req.Name)
	switch {
	case errors.Is(err, ErrValidation):
		writeError(w, http.StatusBadRequest, "validation_error", "Nombre requerido (1..60)")
	case errors.Is(err, ErrNameTaken):
		writeError(w, http.StatusConflict, "name_taken", "Ya existe una sucursal con ese nombre")
	case err != nil:
		writeError(w, http.StatusInternalServerError, "internal", "No se pudo crear la sucursal")
	default:
		writeJSON(w, http.StatusCreated, map[string]any{"branch": b})
	}
}

type updateRequest struct {
	Name   *string `json:"name"`
	Status *string `json:"status"`
}

func (svc *Service) handleUpdate(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := tenantOf(w, r)
	if !ok {
		return
	}
	var req updateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", "Cuerpo inválido")
		return
	}
	b, err := svc.Update(r.Context(), tenantID, chi.URLParam(r, "id"), BranchPatch{Name: req.Name, Status: req.Status})
	switch {
	case errors.Is(err, ErrValidation):
		writeError(w, http.StatusBadRequest, "validation_error", "Nombre (1..60) o estado (active|inactive) inválido")
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "Sucursal no encontrada")
	case errors.Is(err, ErrNameTaken):
		writeError(w, http.StatusConflict, "name_taken", "Ya existe una sucursal con ese nombre")
	case err != nil:
		writeError(w, http.StatusInternalServerError, "internal", "No se pudo actualizar la sucursal")
	default:
		writeJSON(w, http.StatusOK, map[string]any{"branch": b})
	}
}

func (svc *Service) handleDelete(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := tenantOf(w, r)
	if !ok {
		return
	}
	err := svc.Delete(r.Context(), tenantID, chi.URLParam(r, "id"))
	switch {
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "Sucursal no encontrada")
	case errors.Is(err, ErrInUse):
		writeError(w, http.StatusConflict, "branch_in_use", "La sucursal está en uso; desactívala en lugar de borrarla")
	case err != nil:
		writeError(w, http.StatusInternalServerError, "internal", "No se pudo borrar la sucursal")
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}
