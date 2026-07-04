package products

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"faro/internal/auth"
)

// Routes devuelve el router de productos (se monta en /products). Lectura (GET) =
// cualquier usuario del negocio; escritura = solo super admin (matriz §7).
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
	Name       string  `json:"name"`
	PriceCents int     `json:"priceCents"`
	CategoryID *string `json:"categoryId"`
	ImageURL   *string `json:"imageUrl"`
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
	p, err := svc.Create(r.Context(), tenantID, CreateInput{Name: req.Name, PriceCents: req.PriceCents, CategoryID: req.CategoryID, ImageURL: req.ImageURL})
	writeResult(w, p, err, http.StatusCreated)
}

func (svc *Service) handleList(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := auth.ResolveTenant(w, r)
	if !ok {
		return
	}
	items, err := svc.List(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "No se pudieron listar los productos")
		return
	}
	if items == nil {
		items = []Product{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (svc *Service) handleGet(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := auth.ResolveTenant(w, r)
	if !ok {
		return
	}
	p, err := svc.Get(r.Context(), tenantID, chi.URLParam(r, "id"))
	switch {
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "Producto no encontrado")
	case err != nil:
		writeError(w, http.StatusInternalServerError, "internal", "No se pudo obtener el producto")
	default:
		writeJSON(w, http.StatusOK, map[string]any{"product": p})
	}
}

type updateRequest struct {
	Name       *string `json:"name"`
	PriceCents *int    `json:"priceCents"`
	CategoryID *string `json:"categoryId"`
	Status     *string `json:"status"`
	ImageURL   *string `json:"imageUrl"`
}

func (svc *Service) handleUpdate(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := auth.ResolveTenant(w, r)
	if !ok {
		return
	}
	var req updateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", "Cuerpo inválido")
		return
	}
	p, err := svc.Update(r.Context(), tenantID, chi.URLParam(r, "id"), UpdateInput{
		Name: req.Name, PriceCents: req.PriceCents, CategoryID: req.CategoryID, Status: req.Status, ImageURL: req.ImageURL,
	})
	writeResult(w, p, err, http.StatusOK)
}

// writeResult traduce el resultado del service a HTTP (errores comunes a crear/editar).
func writeResult(w http.ResponseWriter, p Product, err error, okStatus int) {
	switch {
	case errors.Is(err, ErrValidation):
		writeError(w, http.StatusBadRequest, "validation_error", "Datos inválidos (nombre y precio > 0)")
	case errors.Is(err, ErrInvalidCategory):
		writeError(w, http.StatusBadRequest, "invalid_category", "La categoría no pertenece a este negocio")
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "Producto no encontrado")
	case errors.Is(err, ErrNameTaken):
		writeError(w, http.StatusConflict, "name_taken", "Ya existe un producto con ese nombre")
	case err != nil:
		writeError(w, http.StatusInternalServerError, "internal", "No se pudo completar la operación")
	default:
		writeJSON(w, okStatus, map[string]any{"product": p})
	}
}
