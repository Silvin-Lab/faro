package agreementdiscounts

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"faro/internal/auth"
)

// Routes se monta en /agreement-discounts. Lectura (listar/ver) = cualquier sesión
// (el POS lee los botones de convenio); CRUD = solo super admin (ADR-010 §3.1).
func (svc *Service) Routes(requireSession, requireSuperAdmin func(http.Handler) http.Handler) http.Handler {
	r := chi.NewRouter()
	r.Group(func(r chi.Router) {
		r.Use(requireSession)
		r.Get("/", svc.handleList)
		r.Get("/{id}", svc.handleGet)
	})
	r.Group(func(r chi.Router) {
		r.Use(requireSuperAdmin)
		r.Post("/", svc.handleCreate)
		r.Put("/{id}", svc.handleUpdate)
		r.Delete("/{id}", svc.handleArchive)
	})
	return r
}

type discountRequest struct {
	Percent int `json:"percent"`
}

func (svc *Service) handleList(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := auth.ResolveTenant(w, r)
	if !ok {
		return
	}
	items, err := svc.List(r.Context(), tenantID, r.URL.Query().Get("status"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "No se pudieron listar los descuentos de convenio")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (svc *Service) handleGet(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := auth.ResolveTenant(w, r)
	if !ok {
		return
	}
	d, err := svc.Get(r.Context(), tenantID, chi.URLParam(r, "id"))
	switch {
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "Descuento de convenio no encontrado")
	case err != nil:
		writeError(w, http.StatusInternalServerError, "internal", "No se pudo obtener el descuento de convenio")
	default:
		writeJSON(w, http.StatusOK, map[string]any{"discount": d})
	}
}

func (svc *Service) handleCreate(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := auth.ResolveTenant(w, r)
	if !ok {
		return
	}
	var req discountRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", "Cuerpo inválido")
		return
	}
	d, err := svc.Create(r.Context(), tenantID, req.Percent)
	switch {
	case errors.Is(err, ErrValidation):
		writeError(w, http.StatusBadRequest, "validation_error", "El porcentaje debe ser un entero entre 1 y 100")
	case errors.Is(err, ErrDuplicate):
		writeError(w, http.StatusConflict, "agreement_discount_duplicate", "Ya existe un descuento activo con ese porcentaje")
	case err != nil:
		writeError(w, http.StatusInternalServerError, "internal", "No se pudo crear el descuento de convenio")
	default:
		writeJSON(w, http.StatusCreated, map[string]any{"discount": d})
	}
}

func (svc *Service) handleUpdate(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := auth.ResolveTenant(w, r)
	if !ok {
		return
	}
	var req discountRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", "Cuerpo inválido")
		return
	}
	d, err := svc.Update(r.Context(), tenantID, chi.URLParam(r, "id"), req.Percent)
	switch {
	case errors.Is(err, ErrValidation):
		writeError(w, http.StatusBadRequest, "validation_error", "El porcentaje debe ser un entero entre 1 y 100")
	case errors.Is(err, ErrDuplicate):
		writeError(w, http.StatusConflict, "agreement_discount_duplicate", "Ya existe un descuento activo con ese porcentaje")
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "Descuento de convenio no encontrado")
	case err != nil:
		writeError(w, http.StatusInternalServerError, "internal", "No se pudo actualizar el descuento de convenio")
	default:
		writeJSON(w, http.StatusOK, map[string]any{"discount": d})
	}
}

func (svc *Service) handleArchive(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := auth.ResolveTenant(w, r)
	if !ok {
		return
	}
	err := svc.Archive(r.Context(), tenantID, chi.URLParam(r, "id"))
	switch {
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "Descuento de convenio no encontrado")
	case err != nil:
		writeError(w, http.StatusInternalServerError, "internal", "No se pudo archivar el descuento de convenio")
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}
