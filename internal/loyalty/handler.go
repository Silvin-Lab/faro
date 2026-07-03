package loyalty

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"faro/internal/auth"
)

// Routes se monta en /loyalty. Requiere sesión.
func (svc *Service) Routes(requireSession func(http.Handler) http.Handler) http.Handler {
	r := chi.NewRouter()
	r.Use(requireSession)
	r.Get("/promotions", svc.handleList)
	r.Post("/promotions", svc.handleCreate)
	r.Get("/promotions/{id}", svc.handleGet)
	r.Put("/promotions/{id}", svc.handleUpdate)
	r.Delete("/promotions/{id}", svc.handleArchive)
	r.Get("/customers/{customerId}/status", svc.handleCustomerStatus)
	return r
}

func tenantOf(w http.ResponseWriter, r *http.Request) (string, bool) {
	u, ok := auth.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "No autenticado")
		return "", false
	}
	if u.TenantID == nil {
		writeError(w, http.StatusBadRequest, "tenant_required", "Esta operación requiere un negocio")
		return "", false
	}
	return *u.TenantID, true
}

type promotionRequest struct {
	Name            string   `json:"name"`
	DiscountPercent int      `json:"discountPercent"`
	VisitThreshold  int      `json:"visitThreshold"`
	ResetsCounter   bool     `json:"resetsCounter"`
	ProductIDs      []string `json:"productIds"`
}

func (req promotionRequest) toInput() PromotionInput {
	return PromotionInput{
		Name:            req.Name,
		DiscountPercent: req.DiscountPercent,
		VisitThreshold:  req.VisitThreshold,
		ResetsCounter:   req.ResetsCounter,
		ProductIDs:      req.ProductIDs,
	}
}

func (svc *Service) handleList(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := tenantOf(w, r)
	if !ok {
		return
	}
	items, err := svc.List(r.Context(), tenantID, r.URL.Query().Get("status"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "No se pudieron listar las promociones")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (svc *Service) handleCreate(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := tenantOf(w, r)
	if !ok {
		return
	}
	var req promotionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", "Cuerpo inválido")
		return
	}
	p, err := svc.Create(r.Context(), tenantID, req.toInput())
	switch {
	case errors.Is(err, ErrValidation):
		writeError(w, http.StatusBadRequest, "validation_error", "Promoción inválida (nombre, % 1-100, umbral > 0, y productos del negocio)")
	case err != nil:
		writeError(w, http.StatusInternalServerError, "internal", "No se pudo crear la promoción")
	default:
		writeJSON(w, http.StatusCreated, map[string]any{"promotion": p})
	}
}

func (svc *Service) handleGet(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := tenantOf(w, r)
	if !ok {
		return
	}
	p, err := svc.Get(r.Context(), tenantID, chi.URLParam(r, "id"))
	switch {
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "Promoción no encontrada")
	case err != nil:
		writeError(w, http.StatusInternalServerError, "internal", "No se pudo obtener la promoción")
	default:
		writeJSON(w, http.StatusOK, map[string]any{"promotion": p})
	}
}

func (svc *Service) handleUpdate(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := tenantOf(w, r)
	if !ok {
		return
	}
	var req promotionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", "Cuerpo inválido")
		return
	}
	p, err := svc.Update(r.Context(), tenantID, chi.URLParam(r, "id"), req.toInput())
	switch {
	case errors.Is(err, ErrValidation):
		writeError(w, http.StatusBadRequest, "validation_error", "Promoción inválida (nombre, % 1-100, umbral > 0, y productos del negocio)")
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "Promoción no encontrada")
	case err != nil:
		writeError(w, http.StatusInternalServerError, "internal", "No se pudo actualizar la promoción")
	default:
		writeJSON(w, http.StatusOK, map[string]any{"promotion": p})
	}
}

func (svc *Service) handleArchive(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := tenantOf(w, r)
	if !ok {
		return
	}
	err := svc.Archive(r.Context(), tenantID, chi.URLParam(r, "id"))
	switch {
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "Promoción no encontrada")
	case err != nil:
		writeError(w, http.StatusInternalServerError, "internal", "No se pudo archivar la promoción")
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

func (svc *Service) handleCustomerStatus(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := tenantOf(w, r)
	if !ok {
		return
	}
	st, err := svc.CustomerStatus(r.Context(), tenantID, chi.URLParam(r, "customerId"))
	switch {
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "Cliente no encontrado")
	case err != nil:
		writeError(w, http.StatusInternalServerError, "internal", "No se pudo obtener el estado de lealtad")
	default:
		writeJSON(w, http.StatusOK, st)
	}
}
