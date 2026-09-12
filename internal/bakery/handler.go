package bakery

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"faro/internal/auth"
)

// Routes se monta en /bakery bajo RequireSession. La autorización es INLINE por rol en
// cada handler (5 roles con permisos distintos): sucursal, repostero, super_admin
// (tech-spec §5/§7.4).
func (svc *Service) Routes(requireSession func(http.Handler) http.Handler) http.Handler {
	r := chi.NewRouter()
	r.Use(requireSession)
	r.Post("/orders", svc.handleCreateOrder)
	r.Get("/orders", svc.handleListOrders)
	r.Get("/orders/{id}", svc.handleGetOrder)
	r.Post("/orders/{id}/produce", svc.handleProduce)
	r.Patch("/orders/{id}/cancel", svc.handleCancel)
	r.Patch("/orders/{id}/receive", svc.handleReceive)
	r.Get("/stock", svc.handleListStock)
	r.Get("/productions", svc.handleListProductions)
	return r
}

// ---- Helpers de rol --------------------------------------------------------

// isBranchUser indica si el usuario es de sucursal (tiene activeBranch): branch_admin,
// cashier o barista.
func isBranchUser(u auth.User) bool {
	return u.Role == auth.RoleBranchAdmin || u.Role == auth.RoleCashier || u.Role == auth.RoleBarista
}

// isProduction indica si el usuario ve/opera la producción de todas las sucursales:
// super_admin o repostero.
func isProduction(u auth.User) bool {
	return u.IsSuperAdmin || u.Role == auth.RoleRepostero
}

// user recupera el usuario en sesión; escribe 401 y devuelve false si no hay.
func user(w http.ResponseWriter, r *http.Request) (auth.User, bool) {
	u, ok := auth.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "Sesión requerida")
		return auth.User{}, false
	}
	return u, true
}

// ---- Crear pedido (§5.1, F3/F4) --------------------------------------------

type createOrderRequest struct {
	ProductID string  `json:"productId"`
	Quantity  int     `json:"quantity"`
	Note      *string `json:"note"`
}

func (svc *Service) handleCreateOrder(w http.ResponseWriter, r *http.Request) {
	u, ok := user(w, r)
	if !ok {
		return
	}
	// Solo sucursal: super_admin/repostero no tienen sucursal a nombre de quién pedir.
	if !isBranchUser(u) {
		writeError(w, http.StatusForbidden, "forbidden", "Solo el personal de sucursal puede crear pedidos")
		return
	}
	tenantID, ok := auth.ResolveTenant(w, r)
	if !ok {
		return
	}
	active, _ := auth.ActiveBranchFromContext(r.Context())
	if active == nil || *active == "" {
		writeError(w, http.StatusBadRequest, "invalid_branch", "Selecciona una sucursal activa")
		return
	}
	var req createOrderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", "Cuerpo inválido")
		return
	}
	order, err := svc.CreateOrder(r.Context(), tenantID, *active, req.ProductID, req.Quantity, req.Note, u.ID)
	switch {
	case errors.Is(err, ErrValidation):
		writeError(w, http.StatusBadRequest, "validation_error", "La cantidad debe ser mayor a 0")
	case errors.Is(err, ErrInvalidBranch):
		writeError(w, http.StatusBadRequest, "invalid_branch", "La sucursal no es válida")
	case errors.Is(err, ErrInvalidProduct):
		writeError(w, http.StatusBadRequest, "invalid_product", "El producto no es un postre de repostería activo")
	case err != nil:
		writeError(w, http.StatusInternalServerError, "internal", "No se pudo crear el pedido")
	default:
		writeJSON(w, http.StatusCreated, map[string]any{"order": order})
	}
}

// ---- Listar pedidos / cola (§5.2, F5/F6) -----------------------------------

func (svc *Service) handleListOrders(w http.ResponseWriter, r *http.Request) {
	u, ok := user(w, r)
	if !ok {
		return
	}
	tenantID, ok := auth.ResolveTenant(w, r)
	if !ok {
		return
	}
	statuses := parseStatuses(r.URL.Query().Get("status"))
	q := r.URL.Query().Get("q")

	var branchFilter *string
	if isProduction(u) {
		// Repostero / super_admin: todas las sucursales; ?branchId opcional acota.
		if b := r.URL.Query().Get("branchId"); b != "" {
			branchFilter = &b
		}
	} else if isBranchUser(u) {
		// Sucursal: forzado a su sucursal activa; se ignora ?branchId.
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

	items, err := svc.ListOrders(r.Context(), tenantID, branchFilter, statuses, q)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "No se pudieron listar los pedidos")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// ---- Detalle de pedido + producciones (§5.3) -------------------------------

func (svc *Service) handleGetOrder(w http.ResponseWriter, r *http.Request) {
	u, ok := user(w, r)
	if !ok {
		return
	}
	tenantID, ok := auth.ResolveTenant(w, r)
	if !ok {
		return
	}
	id := chi.URLParam(r, "id")
	order, err := svc.GetOrder(r.Context(), tenantID, id)
	switch {
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "Pedido no encontrado")
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, "internal", "No se pudo obtener el pedido")
		return
	}
	// Autorización de lectura: producción (todas) o la sucursal dueña.
	if !isProduction(u) {
		active, _ := auth.ActiveBranchFromContext(r.Context())
		if !(isBranchUser(u) && active != nil && *active == order.BranchID) {
			writeError(w, http.StatusForbidden, "forbidden", "No autorizado para ver este pedido")
			return
		}
	}
	productions, err := svc.ProductionsForOrder(r.Context(), tenantID, order.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "No se pudieron obtener las producciones")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"order": order, "productions": productions})
}

// ---- Registrar producción (§5.5, F7-F11) -----------------------------------

type produceRequest struct {
	Quantity int `json:"quantity"`
}

func (svc *Service) handleProduce(w http.ResponseWriter, r *http.Request) {
	u, ok := user(w, r)
	if !ok {
		return
	}
	// Solo producción (repostero/super_admin); sucursal => 403.
	if !isProduction(u) {
		writeError(w, http.StatusForbidden, "forbidden", "Solo la repostería puede registrar producción")
		return
	}
	tenantID, ok := auth.ResolveTenant(w, r)
	if !ok {
		return
	}
	var req produceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", "Cuerpo inválido")
		return
	}
	order, production, err := svc.Produce(r.Context(), tenantID, chi.URLParam(r, "id"), req.Quantity, u.ID)
	switch {
	case errors.Is(err, ErrValidation):
		writeError(w, http.StatusBadRequest, "validation_error", "La cantidad debe ser mayor a 0")
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "Pedido no encontrado")
	case errors.Is(err, ErrInvalidState):
		writeError(w, http.StatusConflict, "invalid_state", "El pedido no admite producción en su estado actual")
	case err != nil:
		writeError(w, http.StatusInternalServerError, "internal", "No se pudo registrar la producción")
	default:
		writeJSON(w, http.StatusOK, map[string]any{"order": order, "production": production})
	}
}

// ---- Cancelar (§5.4, F12/F13) ----------------------------------------------

func (svc *Service) handleCancel(w http.ResponseWriter, r *http.Request) {
	svc.transitionOwned(w, r, svc.CancelOrder, "No se pudo cancelar el pedido")
}

// ---- Marcar recibido (§5.6, F14) -------------------------------------------

func (svc *Service) handleReceive(w http.ResponseWriter, r *http.Request) {
	svc.transitionOwned(w, r, svc.ReceiveOrder, "No se pudo marcar el pedido como recibido")
}

// transitionOwned aplica una transición de estado (cancel/receive) restringida a la
// sucursal dueña o super_admin (repostero => 403). Comparte el gating y el mapeo de
// errores de ambos endpoints.
func (svc *Service) transitionOwned(w http.ResponseWriter, r *http.Request, action func(ctx context.Context, tenantID, id string) (Order, error), internalMsg string) {
	u, ok := user(w, r)
	if !ok {
		return
	}
	tenantID, ok := auth.ResolveTenant(w, r)
	if !ok {
		return
	}
	id := chi.URLParam(r, "id")
	order, err := svc.GetOrder(r.Context(), tenantID, id)
	switch {
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "Pedido no encontrado")
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, "internal", internalMsg)
		return
	}
	// Solo la sucursal dueña o super_admin (repostero => 403).
	if !u.IsSuperAdmin {
		active, _ := auth.ActiveBranchFromContext(r.Context())
		if !(isBranchUser(u) && active != nil && *active == order.BranchID) {
			writeError(w, http.StatusForbidden, "forbidden", "No autorizado sobre este pedido")
			return
		}
	}
	updated, err := action(r.Context(), tenantID, id)
	switch {
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "Pedido no encontrado")
	case errors.Is(err, ErrInvalidState):
		writeError(w, http.StatusConflict, "invalid_state", "El pedido no admite esta acción en su estado actual")
	case err != nil:
		writeError(w, http.StatusInternalServerError, "internal", internalMsg)
	default:
		writeJSON(w, http.StatusOK, map[string]any{"order": updated})
	}
}

// ---- Stock de postres (§5.7, F15) ------------------------------------------

func (svc *Service) handleListStock(w http.ResponseWriter, r *http.Request) {
	u, ok := user(w, r)
	if !ok {
		return
	}
	tenantID, ok := auth.ResolveTenant(w, r)
	if !ok {
		return
	}
	q := r.URL.Query().Get("q")

	scope := "all"
	var branchFilter *string
	if isProduction(u) {
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
		scope = "branch"
	} else {
		writeError(w, http.StatusForbidden, "forbidden", "No autorizado")
		return
	}

	items, err := svc.ListStock(r.Context(), tenantID, branchFilter, q)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "No se pudo obtener el stock de postres")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"scope": scope, "items": items})
}

// ---- Auditoría de producciones (§5.8) --------------------------------------

func (svc *Service) handleListProductions(w http.ResponseWriter, r *http.Request) {
	u, ok := user(w, r)
	if !ok {
		return
	}
	if !isProduction(u) {
		writeError(w, http.StatusForbidden, "forbidden", "No autorizado")
		return
	}
	tenantID, ok := auth.ResolveTenant(w, r)
	if !ok {
		return
	}
	from := parseTime(r.URL.Query().Get("from"))
	to := parseTime(r.URL.Query().Get("to"))
	var branchID, productID *string
	if b := r.URL.Query().Get("branchId"); b != "" {
		branchID = &b
	}
	if p := r.URL.Query().Get("productId"); p != "" {
		productID = &p
	}
	items, err := svc.ListProductions(r.Context(), tenantID, from, to, branchID, productID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "No se pudieron listar las producciones")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// parseTime acepta RFC3339; inválida/ausente => nil.
func parseTime(s string) *time.Time {
	if s == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return nil
	}
	return &t
}
