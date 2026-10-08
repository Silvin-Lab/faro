package requisitions

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"faro/internal/auth"
)

// Routes se monta en /requisitions bajo RequireSession. Autorización INLINE por rol en cada
// handler: sucursal (crea/ve/cancela la suya, ve sugerencias), super_admin (ve todas,
// cierra). repostero => 403 (no participa en requisiciones de insumos).
func (svc *Service) Routes(requireSession func(http.Handler) http.Handler) http.Handler {
	r := chi.NewRouter()
	r.Use(requireSession)
	r.Post("/supplies", svc.handleCreate)
	r.Get("/supplies", svc.handleList)
	// Estático antes que el parámetro (chi prioriza estáticos, pero se registra explícito).
	r.Get("/supplies/suggestions", svc.handleSuggestions)
	r.Get("/supplies/{id}", svc.handleGet)
	r.Patch("/supplies/{id}/cancel", svc.handleCancel)
	r.Patch("/supplies/{id}/close", svc.handleClose)
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

// ---- Crear (solo sucursal) -------------------------------------------------

type createRequest struct {
	Note  *string `json:"note"`
	Items []struct {
		SupplyID     string  `json:"supplyId"`
		QuantityBase int     `json:"quantityBase"`
		Note         *string `json:"note"`
	} `json:"items"`
}

func (svc *Service) handleCreate(w http.ResponseWriter, r *http.Request) {
	u, ok := user(w, r)
	if !ok {
		return
	}
	// Decisión de diseño: la requisición la crea SIEMPRE la sucursal (a nombre de su
	// sucursal activa). El super_admin resuelve/surte, no solicita => 403 si intenta crear.
	if !isBranchUser(u) {
		writeError(w, http.StatusForbidden, "forbidden", "Solo el personal de sucursal puede crear requisiciones")
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
	var req createRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", "Cuerpo inválido")
		return
	}
	items := make([]LineInput, 0, len(req.Items))
	for _, it := range req.Items {
		items = append(items, LineInput{SupplyID: it.SupplyID, QuantityBase: it.QuantityBase, Note: it.Note})
	}
	d, err := svc.CreateRequisition(r.Context(), tenantID, *active, req.Note, items, u.ID)
	switch {
	case errors.Is(err, ErrValidation):
		writeError(w, http.StatusBadRequest, "validation_error", "Datos de la requisición inválidos")
	case errors.Is(err, ErrInvalidBranch):
		writeError(w, http.StatusBadRequest, "invalid_branch", "La sucursal no es válida")
	case errors.Is(err, ErrInvalidSupply):
		writeError(w, http.StatusBadRequest, "invalid_supply", "Un insumo no es válido")
	case err != nil:
		writeError(w, http.StatusInternalServerError, "internal", "No se pudo crear la requisición")
	default:
		writeJSON(w, http.StatusCreated, d)
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
	var status *string
	if s := r.URL.Query().Get("status"); s != "" {
		status = &s
	}
	items, err := svc.ListRequisitions(r.Context(), tenantID, branchFilter, status, r.URL.Query().Get("q"))
	switch {
	case errors.Is(err, ErrValidation):
		writeError(w, http.StatusBadRequest, "validation_error", "status inválido")
	case err != nil:
		writeError(w, http.StatusInternalServerError, "internal", "No se pudieron listar las requisiciones")
	default:
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
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
	d, err := svc.GetRequisition(r.Context(), tenantID, chi.URLParam(r, "id"))
	switch {
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "Requisición no encontrada")
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, "internal", "No se pudo obtener la requisición")
		return
	}
	if !u.IsSuperAdmin {
		active, _ := auth.ActiveBranchFromContext(r.Context())
		if !(isBranchUser(u) && active != nil && *active == d.Requisition.BranchID) {
			writeError(w, http.StatusForbidden, "forbidden", "No autorizado para ver esta requisición")
			return
		}
	}
	writeJSON(w, http.StatusOK, d)
}

// ---- Cancelar (dueña o super_admin, solo pending) --------------------------

func (svc *Service) handleCancel(w http.ResponseWriter, r *http.Request) {
	u, ok := user(w, r)
	if !ok {
		return
	}
	tenantID, ok := auth.ResolveTenant(w, r)
	if !ok {
		return
	}
	id := chi.URLParam(r, "id")
	// Gating: super_admin o la sucursal dueña.
	branchID, err := svc.RequisitionBranch(r.Context(), tenantID, id)
	switch {
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "Requisición no encontrada")
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, "internal", "No se pudo cancelar la requisición")
		return
	}
	if !u.IsSuperAdmin {
		active, _ := auth.ActiveBranchFromContext(r.Context())
		if !(isBranchUser(u) && active != nil && *active == branchID) {
			writeError(w, http.StatusForbidden, "forbidden", "No autorizado sobre esta requisición")
			return
		}
	}
	d, err := svc.CancelRequisition(r.Context(), tenantID, id)
	switch {
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "Requisición no encontrada")
	case errors.Is(err, ErrInvalidState):
		writeError(w, http.StatusConflict, "invalid_state", "Solo se puede cancelar una requisición pendiente sin surtir")
	case err != nil:
		writeError(w, http.StatusInternalServerError, "internal", "No se pudo cancelar la requisición")
	default:
		writeJSON(w, http.StatusOK, d)
	}
}

// ---- Cerrar (solo super_admin, partial->fulfilled) -------------------------

func (svc *Service) handleClose(w http.ResponseWriter, r *http.Request) {
	u, ok := user(w, r)
	if !ok {
		return
	}
	if !u.IsSuperAdmin {
		writeError(w, http.StatusForbidden, "forbidden", "Solo el administrador puede cerrar una requisición")
		return
	}
	tenantID, ok := auth.ResolveTenant(w, r)
	if !ok {
		return
	}
	d, err := svc.CloseRequisition(r.Context(), tenantID, chi.URLParam(r, "id"))
	switch {
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "Requisición no encontrada")
	case errors.Is(err, ErrInvalidState):
		writeError(w, http.StatusConflict, "invalid_state", "Solo se puede cerrar una requisición parcialmente surtida")
	case err != nil:
		writeError(w, http.StatusInternalServerError, "internal", "No se pudo cerrar la requisición")
	default:
		writeJSON(w, http.StatusOK, d)
	}
}

// ---- Sugerencias (solo sucursal, su propia sucursal activa) ----------------

func (svc *Service) handleSuggestions(w http.ResponseWriter, r *http.Request) {
	u, ok := user(w, r)
	if !ok {
		return
	}
	if !isBranchUser(u) {
		writeError(w, http.StatusForbidden, "forbidden", "Solo el personal de sucursal ve sugerencias de reposición")
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
	items, err := svc.Suggestions(r.Context(), tenantID, *active)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "No se pudieron obtener las sugerencias")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}
