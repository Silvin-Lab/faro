package dayclose

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"faro/internal/auth"
)

// Routes se monta en /dayclose bajo RequireSession. Autorización INLINE por rol: cualquier
// rol de sucursal opera el cierre de SU sucursal activa (el corte lo hace el equipo de
// piso, no solo branch_admin); super_admin ve todos. repostero => 403.
func (svc *Service) Routes(requireSession func(http.Handler) http.Handler) http.Handler {
	r := chi.NewRouter()
	r.Use(requireSession)
	r.Post("/open", svc.handleOpen)
	r.Get("/", svc.handleList)
	r.Get("/{id}", svc.handleGet)
	r.Patch("/{id}", svc.handlePatch)
	r.Post("/{id}/submit", svc.handleSubmit)
	return r
}

// ---- Helpers de rol --------------------------------------------------------

func isBranchUser(u auth.User) bool {
	return u.Role == auth.RoleBranchAdmin || u.Role == auth.RoleCashier || u.Role == auth.RoleBarista
}

func user(w http.ResponseWriter, r *http.Request) (auth.User, bool) {
	u, ok := auth.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "Sesión requerida")
		return auth.User{}, false
	}
	return u, true
}

// ---- Abrir (get-or-create) -------------------------------------------------

type openRequest struct {
	Date string `json:"date"`
}

func (svc *Service) handleOpen(w http.ResponseWriter, r *http.Request) {
	u, ok := user(w, r)
	if !ok {
		return
	}
	if !isBranchUser(u) {
		writeError(w, http.StatusForbidden, "forbidden", "Solo el personal de sucursal abre el cierre de día")
		return
	}
	tenantID, ok := auth.ResolveTenant(w, r)
	if !ok {
		return
	}
	active, _ := auth.ActiveBranchFromContext(r.Context())
	if active == nil || *active == "" {
		writeError(w, http.StatusBadRequest, "branch_required", "Selecciona una sucursal activa")
		return
	}
	var req openRequest
	// El body es opcional (date). Un cuerpo vacío es válido.
	_ = json.NewDecoder(r.Body).Decode(&req)
	c, err := svc.Open(r.Context(), tenantID, *active, req.Date, u.ID)
	switch {
	case errors.Is(err, ErrValidation):
		writeError(w, http.StatusBadRequest, "validation_error", "Fecha inválida")
	case err != nil:
		writeError(w, http.StatusInternalServerError, "internal", "No se pudo abrir el cierre de día")
	default:
		writeJSON(w, http.StatusOK, map[string]any{"closure": c})
	}
}

// ---- Detalle ---------------------------------------------------------------

func (svc *Service) handleGet(w http.ResponseWriter, r *http.Request) {
	u, ok := user(w, r)
	if !ok {
		return
	}
	tenantID, ok := auth.ResolveTenant(w, r)
	if !ok {
		return
	}
	id := chi.URLParam(r, "id")
	raw, err := svc.GetRaw(r.Context(), tenantID, id)
	switch {
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "Cierre no encontrado")
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, "internal", "No se pudo obtener el cierre")
		return
	}
	if !svc.authorizeOwner(w, r, u, raw.BranchID) {
		return
	}
	c, err := svc.Get(r.Context(), tenantID, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "No se pudo obtener el cierre")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"closure": c})
}

// ---- Editar borrador -------------------------------------------------------

type patchRequest struct {
	CashCountedCents    *int    `json:"cashCountedCents"`
	Notes               *string `json:"notes"`
	BakeryCountID       *string `json:"bakeryCountId"`
	SupplyRequisitionID *string `json:"supplyRequisitionId"`
}

func (svc *Service) handlePatch(w http.ResponseWriter, r *http.Request) {
	u, ok := user(w, r)
	if !ok {
		return
	}
	tenantID, ok := auth.ResolveTenant(w, r)
	if !ok {
		return
	}
	id := chi.URLParam(r, "id")
	raw, err := svc.GetRaw(r.Context(), tenantID, id)
	switch {
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "Cierre no encontrado")
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, "internal", "No se pudo obtener el cierre")
		return
	}
	if !svc.authorizeOwner(w, r, u, raw.BranchID) {
		return
	}
	var req patchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", "Cuerpo inválido")
		return
	}
	c, err := svc.UpdateDraft(r.Context(), tenantID, id, DraftPatch{
		CashCountedCents:    req.CashCountedCents,
		Notes:               req.Notes,
		BakeryCountID:       req.BakeryCountID,
		SupplyRequisitionID: req.SupplyRequisitionID,
	})
	switch {
	case errors.Is(err, ErrValidation):
		writeError(w, http.StatusBadRequest, "validation_error", "Datos del cierre inválidos")
	case errors.Is(err, ErrInvalidState):
		writeError(w, http.StatusConflict, "invalid_state", "El cierre ya fue enviado y no puede editarse")
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "Cierre no encontrado")
	case err != nil:
		writeError(w, http.StatusInternalServerError, "internal", "No se pudo actualizar el cierre")
	default:
		writeJSON(w, http.StatusOK, map[string]any{"closure": c})
	}
}

// ---- Enviar (submit) -------------------------------------------------------

func (svc *Service) handleSubmit(w http.ResponseWriter, r *http.Request) {
	u, ok := user(w, r)
	if !ok {
		return
	}
	tenantID, ok := auth.ResolveTenant(w, r)
	if !ok {
		return
	}
	id := chi.URLParam(r, "id")
	raw, err := svc.GetRaw(r.Context(), tenantID, id)
	switch {
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "Cierre no encontrado")
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, "internal", "No se pudo enviar el cierre")
		return
	}
	if !svc.authorizeOwner(w, r, u, raw.BranchID) {
		return
	}
	c, err := svc.Submit(r.Context(), tenantID, id, u.ID)
	switch {
	case errors.Is(err, ErrInvalidState):
		writeError(w, http.StatusConflict, "invalid_state", "El cierre ya fue enviado")
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "Cierre no encontrado")
	case err != nil:
		writeError(w, http.StatusInternalServerError, "internal", "No se pudo enviar el cierre")
	default:
		writeJSON(w, http.StatusOK, map[string]any{"closure": c})
	}
}

// ---- Listar ----------------------------------------------------------------

func (svc *Service) handleList(w http.ResponseWriter, r *http.Request) {
	u, ok := user(w, r)
	if !ok {
		return
	}
	tenantID, ok := auth.ResolveTenant(w, r)
	if !ok {
		return
	}
	var branchFilter *string
	if u.IsSuperAdmin {
		if b := r.URL.Query().Get("branchId"); b != "" {
			branchFilter = &b
		}
	} else if isBranchUser(u) {
		active, _ := auth.ActiveBranchFromContext(r.Context())
		if active == nil || *active == "" {
			writeError(w, http.StatusBadRequest, "branch_required", "Selecciona una sucursal activa")
			return
		}
		branchFilter = active
	} else {
		writeError(w, http.StatusForbidden, "forbidden", "No autorizado")
		return
	}
	var from, to *string
	if f := r.URL.Query().Get("from"); f != "" {
		from = &f
	}
	if t := r.URL.Query().Get("to"); t != "" {
		to = &t
	}
	items, err := svc.List(r.Context(), tenantID, branchFilter, from, to)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "No se pudieron listar los cierres")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// authorizeOwner exige que el caller sea super_admin o el personal de la sucursal dueña del
// cierre. Escribe 403 y devuelve false si no.
func (svc *Service) authorizeOwner(w http.ResponseWriter, r *http.Request, u auth.User, branchID string) bool {
	if u.IsSuperAdmin {
		return true
	}
	active, _ := auth.ActiveBranchFromContext(r.Context())
	if isBranchUser(u) && active != nil && *active == branchID {
		return true
	}
	writeError(w, http.StatusForbidden, "forbidden", "No autorizado sobre este cierre")
	return false
}
