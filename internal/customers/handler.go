package customers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"faro/internal/auth"
)

// Routes se monta en /customers. Requiere sesión.
func (svc *Service) Routes(requireSession func(http.Handler) http.Handler) http.Handler {
	r := chi.NewRouter()
	r.Use(requireSession)
	r.Post("/", svc.handleCreate)
	r.Get("/", svc.handleSearch)               // ?phone=<exacto> | ?q=<texto>&limit=20
	r.Patch("/{id}/visits", svc.handleSetVisits) // ajuste manual (migración de tarjetas): solo admin
	return r
}

type createRequest struct {
	Phone     string `json:"phone"`
	FirstName string `json:"firstName"`
	LastName  string `json:"lastName"`
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
	c, err := svc.Create(r.Context(), tenantID, req.Phone, req.FirstName, req.LastName)
	switch {
	case errors.Is(err, ErrValidation):
		writeError(w, http.StatusBadRequest, "validation_error", "Teléfono, nombre y apellido son requeridos")
	case errors.Is(err, ErrPhoneTaken):
		writeError(w, http.StatusConflict, "phone_taken", "Ya existe un cliente con ese teléfono")
	case err != nil:
		writeError(w, http.StatusInternalServerError, "internal", "No se pudo registrar el cliente")
	default:
		writeJSON(w, http.StatusCreated, map[string]any{"customer": c})
	}
}

func (svc *Service) handleSearch(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := auth.ResolveTenant(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()

	// Búsqueda por texto (nombre o teléfono): devuelve una lista.
	if q.Has("q") {
		limit, _ := strconv.Atoi(q.Get("limit"))
		items, err := svc.Search(r.Context(), tenantID, q.Get("q"), limit)
		switch {
		case errors.Is(err, ErrValidation):
			writeError(w, http.StatusBadRequest, "validation_error", "Indica un texto de búsqueda")
		case err != nil:
			writeError(w, http.StatusInternalServerError, "internal", "No se pudo buscar clientes")
		default:
			writeJSON(w, http.StatusOK, map[string]any{"items": items})
		}
		return
	}

	// Lookup exacto por teléfono: devuelve un único cliente (retrocompatibilidad).
	c, err := svc.FindByPhone(r.Context(), tenantID, q.Get("phone"))
	switch {
	case errors.Is(err, ErrValidation):
		writeError(w, http.StatusBadRequest, "validation_error", "Indica un teléfono")
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "Cliente no encontrado")
	case err != nil:
		writeError(w, http.StatusInternalServerError, "internal", "No se pudo buscar el cliente")
	default:
		writeJSON(w, http.StatusOK, map[string]any{"customer": c})
	}
}

// setVisitsRequest usa puntero para distinguir "visits" ausente de 0.
type setVisitsRequest struct {
	Visits *int `json:"visits"`
}

// handleSetVisits fija manualmente las visitas del ciclo (migración de tarjetas
// físicas). Solo super_admin o branch_admin; el lifetime nunca decrece (GREATEST).
func (svc *Service) handleSetVisits(w http.ResponseWriter, r *http.Request) {
	u, ok := auth.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "Sesión requerida")
		return
	}
	if u.Role != auth.RoleSuperAdmin && u.Role != auth.RoleBranchAdmin {
		writeError(w, http.StatusForbidden, "forbidden", "No autorizado para ajustar visitas")
		return
	}
	tenantID, ok := auth.ResolveTenant(w, r)
	if !ok {
		return
	}
	var req setVisitsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Visits == nil || *req.Visits < 0 {
		writeError(w, http.StatusBadRequest, "validation_error", "visits debe ser un entero ≥ 0")
		return
	}
	c, err := svc.SetVisits(r.Context(), tenantID, chi.URLParam(r, "id"), *req.Visits)
	switch {
	case errors.Is(err, ErrValidation):
		writeError(w, http.StatusBadRequest, "validation_error", "visits debe ser un entero ≥ 0")
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "Cliente no encontrado")
	case err != nil:
		writeError(w, http.StatusInternalServerError, "internal", "No se pudo actualizar el cliente")
	default:
		writeJSON(w, http.StatusOK, map[string]any{"customer": c})
	}
}
