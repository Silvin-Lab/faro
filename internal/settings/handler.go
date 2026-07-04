package settings

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"faro/internal/auth"
)

// Routes se monta en /settings. Solo super admin (matriz §7): se monta con
// requireSuperAdmin. El tenant se resuelve con ResolveTenant (businessTenantID).
func (svc *Service) Routes(requireSuperAdmin func(http.Handler) http.Handler) http.Handler {
	r := chi.NewRouter()
	r.Use(requireSuperAdmin)
	r.Get("/", svc.handleGet)
	r.Put("/favicon", svc.handleSetFavicon)
	r.Delete("/favicon", svc.handleClearFavicon)
	return r
}

func tenantOf(w http.ResponseWriter, r *http.Request) (string, bool) {
	return auth.ResolveTenant(w, r)
}

func (svc *Service) handleGet(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := tenantOf(w, r)
	if !ok {
		return
	}
	st, err := svc.Get(r.Context(), tenantID)
	switch {
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "Negocio no encontrado")
	case err != nil:
		writeError(w, http.StatusInternalServerError, "internal", "No se pudieron obtener los ajustes")
	default:
		writeJSON(w, http.StatusOK, st)
	}
}

type faviconRequest struct {
	FaviconURL string `json:"faviconUrl"`
}

func (svc *Service) handleSetFavicon(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := tenantOf(w, r)
	if !ok {
		return
	}
	var req faviconRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", "Cuerpo inválido")
		return
	}
	url, err := svc.SetFavicon(r.Context(), tenantID, req.FaviconURL)
	switch {
	case errors.Is(err, ErrValidation):
		writeError(w, http.StatusBadRequest, "validation_error", "faviconUrl debe ser una ruta /files/*")
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "Negocio no encontrado")
	case err != nil:
		writeError(w, http.StatusInternalServerError, "internal", "No se pudo guardar el favicon")
	default:
		writeJSON(w, http.StatusOK, map[string]any{"faviconUrl": url})
	}
}

func (svc *Service) handleClearFavicon(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := tenantOf(w, r)
	if !ok {
		return
	}
	err := svc.ClearFavicon(r.Context(), tenantID)
	switch {
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "Negocio no encontrado")
	case err != nil:
		writeError(w, http.StatusInternalServerError, "internal", "No se pudo quitar el favicon")
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}
