package reports

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"faro/internal/auth"
)

// Routes se monta en /reports. Requiere sesión; la autorización fina depende del
// rol (M8): super admin ve todo (filtro ?branchId opcional), branch_admin queda
// forzado a su sucursal activa, y cashier/barista reciben 403.
func (svc *Service) Routes(requireSession func(http.Handler) http.Handler) http.Handler {
	r := chi.NewRouter()
	r.Use(requireSession)
	r.Get("/sales", svc.handleSalesReport)
	r.Get("/expenses", svc.handleExpensesReport)
	return r
}

// resolveReportScope aplica la autorización por rol (solo super admin y
// branch_admin) y resuelve tenant, rango [from,to) y filtro por sucursal comunes
// a los reportes. Devuelve ok=false tras haber escrito el error HTTP.
func (svc *Service) resolveReportScope(w http.ResponseWriter, r *http.Request) (tenantID string, from, to time.Time, branch BranchFilter, ok bool) {
	u, okU := auth.UserFromContext(r.Context())
	if !okU {
		writeError(w, http.StatusUnauthorized, "unauthorized", "Sesión requerida")
		return
	}
	if !u.IsSuperAdmin && u.Role != auth.RoleBranchAdmin {
		writeError(w, http.StatusForbidden, "forbidden", "No autorizado para ver reportes")
		return
	}
	tenantID, okT := auth.ResolveTenant(w, r)
	if !okT {
		return "", time.Time{}, time.Time{}, BranchFilter{}, false
	}

	now := time.Now()
	from = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	to = from.AddDate(0, 0, 1)
	if f := parseTime(r.URL.Query().Get("from")); f != nil {
		from = *f
	}
	if t := parseTime(r.URL.Query().Get("to")); t != nil {
		to = *t
	}

	if u.IsSuperAdmin {
		if b := r.URL.Query().Get("branchId"); b != "" {
			if b == "none" || b == "null" {
				branch.None = true
			} else {
				branch.ID = &b
			}
		}
	} else {
		active, _ := auth.ActiveBranchFromContext(r.Context())
		if active == nil {
			writeError(w, http.StatusBadRequest, "branch_required", "Selecciona una sucursal activa")
			return "", time.Time{}, time.Time{}, BranchFilter{}, false
		}
		branch.ID = active
	}
	return tenantID, from, to, branch, true
}

func (svc *Service) handleExpensesReport(w http.ResponseWriter, r *http.Request) {
	tenantID, from, to, branch, ok := svc.resolveReportScope(w, r)
	if !ok {
		return
	}
	rep, err := svc.ExpensesReport(r.Context(), tenantID, from, to, branch)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "No se pudo generar el reporte de gastos")
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

func (svc *Service) handleSalesReport(w http.ResponseWriter, r *http.Request) {
	u, ok := auth.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "Sesión requerida")
		return
	}
	// Autorización por rol: solo super admin y branch_admin acceden a reportes.
	if !u.IsSuperAdmin && u.Role != auth.RoleBranchAdmin {
		writeError(w, http.StatusForbidden, "forbidden", "No autorizado para ver reportes")
		return
	}
	tenantID, ok := auth.ResolveTenant(w, r)
	if !ok {
		return
	}

	// Rango: por defecto hoy (hora local del servidor); sobreescribible por from/to.
	now := time.Now()
	from := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	to := from.AddDate(0, 0, 1)
	if f := parseTime(r.URL.Query().Get("from")); f != nil {
		from = *f
	}
	if t := parseTime(r.URL.Query().Get("to")); t != nil {
		to = *t
	}
	tz, _ := strconv.Atoi(r.URL.Query().Get("tz"))

	// Filtro por sucursal.
	var branch BranchFilter
	if u.IsSuperAdmin {
		// Super admin: ?branchId=<uuid> acota; ?branchId=none|null es el bucket
		// "Sin sucursal" (branch_id IS NULL); sin el param => todas.
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
			return
		}
		branch.ID = active
	}

	rep, err := svc.SalesReport(r.Context(), tenantID, from, to, tz, branch)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "No se pudo generar el reporte")
		return
	}
	writeJSON(w, http.StatusOK, rep)
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
