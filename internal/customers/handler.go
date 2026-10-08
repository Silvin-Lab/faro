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
	r.Get("/", svc.handleSearch)                         // ?phone=<exacto> | ?q=<texto>&limit=20 | ?limit=20&offset=0 (listado)
	r.Patch("/{id}/visits", svc.handleSetVisits)         // ajuste manual (migración de tarjetas): solo admin
	r.Get("/{id}/visit-changes", svc.handleVisitChanges) // historial de auditoría: solo admin
	return r
}

// isVisitsAdmin indica si el usuario puede sembrar/ajustar visitas y ver su
// historial: super admin o branch_admin (cajero/barista no).
func isVisitsAdmin(u auth.User) bool {
	return u.IsSuperAdmin || u.Role == auth.RoleSuperAdmin || u.Role == auth.RoleBranchAdmin
}

type createRequest struct {
	Phone       string `json:"phone"`
	FirstName   string `json:"firstName"`
	LastName    string `json:"lastName"`
	PriorVisits int    `json:"priorVisits"` // opcional; ausente en el JSON = 0
}

func (svc *Service) handleCreate(w http.ResponseWriter, r *http.Request) {
	u, ok := auth.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "Sesión requerida")
		return
	}
	tenantID, ok := auth.ResolveTenant(w, r)
	if !ok {
		return
	}
	var req createRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", "Cuerpo inválido")
		return
	}
	// Sembrar visitas al alta (priorVisits > 0) solo lo pueden hacer los admins: de
	// lo contrario un cajero podría inflar visitas para disparar una promo de lealtad.
	// Con 0 (o sin el campo) el alta sigue disponible para todos los roles.
	if req.PriorVisits > 0 && !isVisitsAdmin(u) {
		writeError(w, http.StatusForbidden, "prior_visits_forbidden", "Solo un administrador puede registrar visitas previas")
		return
	}
	c, err := svc.Create(r.Context(), tenantID, req.Phone, req.FirstName, req.LastName, req.PriorVisits, &u.ID)
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

	// Sin texto de búsqueda ni teléfono: listado paginado (default de la pantalla
	// Clientes, "mostrar más" via offset).
	if !q.Has("phone") {
		limit, _ := strconv.Atoi(q.Get("limit"))
		offset, _ := strconv.Atoi(q.Get("offset"))
		items, err := svc.List(r.Context(), tenantID, limit, offset)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal", "No se pudo listar clientes")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
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
	if !isVisitsAdmin(u) {
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
	c, err := svc.SetVisits(r.Context(), tenantID, chi.URLParam(r, "id"), *req.Visits, &u.ID)
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

// handleVisitChanges devuelve el historial de cambios de visitas de un cliente
// (auditoría). Solo super_admin o branch_admin, como el ajuste de visitas.
func (svc *Service) handleVisitChanges(w http.ResponseWriter, r *http.Request) {
	u, ok := auth.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "Sesión requerida")
		return
	}
	if !isVisitsAdmin(u) {
		writeError(w, http.StatusForbidden, "forbidden", "No autorizado para ver el historial de visitas")
		return
	}
	tenantID, ok := auth.ResolveTenant(w, r)
	if !ok {
		return
	}
	items, err := svc.VisitChanges(r.Context(), tenantID, chi.URLParam(r, "id"))
	switch {
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "Cliente no encontrado")
	case err != nil:
		writeError(w, http.StatusInternalServerError, "internal", "No se pudo obtener el historial de visitas")
	default:
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	}
}
