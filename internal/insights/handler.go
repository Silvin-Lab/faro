package insights

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"faro/internal/auth"
)

// Routes se monta en /insights. Requiere sesión; la autorización fina por rol la
// aplica resolveScope: super_admin (filtro ?branchId opcional), branch_admin
// forzado a su sucursal activa, cashier/barista => 403. Las 6 rutas son de solo
// lectura y ninguna llama a un LLM (ADR-009).
func (svc *Service) Routes(requireSession func(http.Handler) http.Handler) http.Handler {
	r := chi.NewRouter()
	r.Use(requireSession)
	r.Get("/recurrence", svc.handleRecurrence)
	r.Get("/top-products", svc.handleTopProducts)
	r.Get("/ticket-segments", svc.handleTicketSegments)
	r.Get("/second-visit", svc.handleSecondVisit)
	r.Get("/basket-affinity", svc.handleBasketAffinity)
	r.Get("/loyalty-effect", svc.handleLoyaltyEffect)
	r.Get("/bakery-trend", svc.handleBakeryTrend)
	return r
}

// resolveScope aplica la autorización por rol (solo super_admin y branch_admin) y
// resuelve tenant, rango [from,to), tz y filtro por sucursal comunes a los 6
// insights. Réplica exacta de reports.resolveReportScope (tech-spec §2). Devuelve
// ok=false tras haber escrito el error HTTP.
func (svc *Service) resolveScope(w http.ResponseWriter, r *http.Request) (Scope, bool) {
	u, okU := auth.UserFromContext(r.Context())
	if !okU {
		writeError(w, http.StatusUnauthorized, "unauthorized", "Sesión requerida")
		return Scope{}, false
	}
	// Autorización por rol: SOLO super_admin y branch_admin. Igual que reports.
	if !u.IsSuperAdmin && u.Role != auth.RoleBranchAdmin {
		writeError(w, http.StatusForbidden, "forbidden", "No autorizado para ver insights")
		return Scope{}, false
	}
	tenantID, okT := auth.ResolveTenant(w, r)
	if !okT {
		return Scope{}, false
	}

	from, to := parseRange(r)
	tz, _ := strconv.Atoi(r.URL.Query().Get("tz"))

	var branch BranchFilter
	if u.IsSuperAdmin {
		// Super admin: ?branchId=<uuid> acota; ?branchId=none|null es el bucket
		// "Sin sucursal"; sin el param => todas.
		if b := r.URL.Query().Get("branchId"); b != "" {
			if b == "none" || b == "null" {
				branch.None = true
			} else {
				branch.ID = &b
			}
		}
	} else {
		// branch_admin: SIEMPRE forzado a su sucursal activa; se ignora ?branchId.
		active, _ := auth.ActiveBranchFromContext(r.Context())
		if active == nil {
			writeError(w, http.StatusBadRequest, "branch_required", "Selecciona una sucursal activa")
			return Scope{}, false
		}
		branch.ID = active
	}
	return Scope{TenantID: tenantID, From: from, To: to, TZ: tz, Branch: branch}, true
}

// parseRange resuelve el rango [from,to). El frontend siempre envía from/to; si
// faltan, mismo fallback defensivo que reports (hoy, hora local del servidor).
func parseRange(r *http.Request) (from, to time.Time) {
	now := time.Now()
	from = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	to = from.AddDate(0, 0, 1)
	if f := parseTime(r.URL.Query().Get("from")); f != nil {
		from = *f
	}
	if t := parseTime(r.URL.Query().Get("to")); t != nil {
		to = *t
	}
	return from, to
}

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

func (svc *Service) handleRecurrence(w http.ResponseWriter, r *http.Request) {
	sc, ok := svc.resolveScope(w, r)
	if !ok {
		return
	}
	res, err := svc.Recurrence(r.Context(), sc)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "No se pudo calcular la recurrencia")
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (svc *Service) handleTopProducts(w http.ResponseWriter, r *http.Request) {
	sc, ok := svc.resolveScope(w, r)
	if !ok {
		return
	}
	res, err := svc.TopProducts(r.Context(), sc)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "No se pudo calcular los productos estrella")
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (svc *Service) handleTicketSegments(w http.ResponseWriter, r *http.Request) {
	sc, ok := svc.resolveScope(w, r)
	if !ok {
		return
	}
	res, err := svc.TicketSegments(r.Context(), sc)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "No se pudo calcular el ticket por segmento")
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (svc *Service) handleSecondVisit(w http.ResponseWriter, r *http.Request) {
	sc, ok := svc.resolveScope(w, r)
	if !ok {
		return
	}
	res, err := svc.SecondVisit(r.Context(), sc)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "No se pudo calcular la 2ª visita")
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (svc *Service) handleBasketAffinity(w http.ResponseWriter, r *http.Request) {
	sc, ok := svc.resolveScope(w, r)
	if !ok {
		return
	}
	res, err := svc.BasketAffinity(r.Context(), sc)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "No se pudo calcular la afinidad de canasta")
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (svc *Service) handleLoyaltyEffect(w http.ResponseWriter, r *http.Request) {
	sc, ok := svc.resolveScope(w, r)
	if !ok {
		return
	}
	res, err := svc.LoyaltyEffect(r.Context(), sc)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "No se pudo calcular la efectividad de lealtad")
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// resolveBakeryScope aplica la autorización por rol de la tendencia de postres (F19), que
// difiere de resolveScope: además de super_admin/branch_admin, INCLUYE al repostero (todas
// las sucursales, sin sucursal activa). cashier/barista => 403. Devuelve ok=false tras
// escribir el error HTTP.
func (svc *Service) resolveBakeryScope(w http.ResponseWriter, r *http.Request) (BakeryScope, bool) {
	u, okU := auth.UserFromContext(r.Context())
	if !okU {
		writeError(w, http.StatusUnauthorized, "unauthorized", "Sesión requerida")
		return BakeryScope{}, false
	}
	// super_admin, branch_admin y repostero pueden ver la tendencia; el resto no.
	if !u.IsSuperAdmin && u.Role != auth.RoleBranchAdmin && u.Role != auth.RoleRepostero {
		writeError(w, http.StatusForbidden, "forbidden", "No autorizado para ver la tendencia de postres")
		return BakeryScope{}, false
	}
	tenantID, okT := auth.ResolveTenant(w, r)
	if !okT {
		return BakeryScope{}, false
	}
	tz, _ := strconv.Atoi(r.URL.Query().Get("tz"))

	var branch BranchFilter
	if u.IsSuperAdmin || u.Role == auth.RoleRepostero {
		// Ven todas las sucursales; ?branchId=<uuid> acota; ?branchId=none|null es el
		// bucket "Sin sucursal".
		if b := r.URL.Query().Get("branchId"); b != "" {
			if b == "none" || b == "null" {
				branch.None = true
			} else {
				branch.ID = &b
			}
		}
	} else {
		// branch_admin: SIEMPRE forzado a su sucursal activa; se ignora ?branchId.
		active, _ := auth.ActiveBranchFromContext(r.Context())
		if active == nil {
			writeError(w, http.StatusBadRequest, "branch_required", "Selecciona una sucursal activa")
			return BakeryScope{}, false
		}
		branch.ID = active
	}
	return BakeryScope{TenantID: tenantID, TZ: tz, Branch: branch}, true
}

func (svc *Service) handleBakeryTrend(w http.ResponseWriter, r *http.Request) {
	sc, ok := svc.resolveBakeryScope(w, r)
	if !ok {
		return
	}
	res, err := svc.BakeryTrend(r.Context(), sc)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "No se pudo calcular la tendencia de postres")
		return
	}
	writeJSON(w, http.StatusOK, res)
}
