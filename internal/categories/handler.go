package categories

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"faro/internal/auth"
)

// Routes devuelve el router de categorías (se monta en /categories). Lectura (GET)
// = cualquier usuario del negocio; escritura = solo super admin (matriz §7).
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
		r.Patch("/{id}", svc.handleUpdate)
	})
	return r
}

type createRequest struct {
	Name      string  `json:"name"`
	SortOrder int     `json:"sortOrder"`
	ImageURL  *string `json:"imageUrl"`
}

func (svc *Service) handleCreate(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := auth.ResolveTenant(w, r)
	if !ok {
		return
	}
	var req createRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", "Cuerpo inválido")
		return
	}
	c, err := svc.Create(r.Context(), tenantID, req.Name, req.SortOrder, req.ImageURL)
	switch {
	case errors.Is(err, ErrValidation):
		writeError(w, http.StatusBadRequest, "validation_error", "El nombre es requerido")
	case errors.Is(err, ErrNameTaken):
		writeError(w, http.StatusConflict, "name_taken", "Ya existe una categoría con ese nombre")
	case err != nil:
		writeError(w, http.StatusInternalServerError, "internal", "No se pudo crear la categoría")
	default:
		writeJSON(w, http.StatusCreated, map[string]any{"category": c})
	}
}

func (svc *Service) handleList(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := auth.ResolveTenant(w, r)
	if !ok {
		return
	}
	items, err := svc.List(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "No se pudieron listar las categorías")
		return
	}
	if items == nil {
		items = []Category{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

type updateRequest struct {
	Name      *string `json:"name"`
	Status    *string `json:"status"`
	SortOrder *int    `json:"sortOrder"`
	ImageURL  *string `json:"imageUrl"`
}

func (svc *Service) handleGet(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := auth.ResolveTenant(w, r)
	if !ok {
		return
	}
	c, err := svc.Get(r.Context(), tenantID, chi.URLParam(r, "id"))
	switch {
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "Categoría no encontrada")
	case err != nil:
		writeError(w, http.StatusInternalServerError, "internal", "No se pudo obtener la categoría")
	default:
		writeJSON(w, http.StatusOK, map[string]any{"category": c})
	}
}

func (svc *Service) handleUpdate(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := auth.ResolveTenant(w, r)
	if !ok {
		return
	}
	id := chi.URLParam(r, "id")
	var req updateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", "Cuerpo inválido")
		return
	}
	c, err := svc.Update(r.Context(), tenantID, id, UpdateInput{Name: req.Name, Status: req.Status, SortOrder: req.SortOrder, ImageURL: req.ImageURL})
	switch {
	case errors.Is(err, ErrValidation):
		writeError(w, http.StatusBadRequest, "validation_error", "Datos inválidos")
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "Categoría no encontrada")
	case errors.Is(err, ErrNameTaken):
		writeError(w, http.StatusConflict, "name_taken", "Ya existe una categoría con ese nombre")
	case err != nil:
		writeError(w, http.StatusInternalServerError, "internal", "No se pudo actualizar la categoría")
	default:
		writeJSON(w, http.StatusOK, map[string]any{"category": c})
	}
}
