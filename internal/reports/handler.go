package reports

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"faro/internal/auth"
)

// Routes se monta en /reports. Solo super admin (matriz §7): se monta con
// requireSuperAdmin. El tenant se resuelve con ResolveTenant (businessTenantID).
func (svc *Service) Routes(requireSuperAdmin func(http.Handler) http.Handler) http.Handler {
	r := chi.NewRouter()
	r.Use(requireSuperAdmin)
	r.Get("/sales", svc.handleSalesReport)
	return r
}

func (svc *Service) handleSalesReport(w http.ResponseWriter, r *http.Request) {
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

	// Filtro por sucursal: ?branchId=<uuid> acota; ?branchId=none|null es el bucket
	// "Sin sucursal" (branch_id IS NULL); sin el param => todas.
	var branch BranchFilter
	if b := r.URL.Query().Get("branchId"); b != "" {
		if b == "none" || b == "null" {
			branch.None = true
		} else {
			branch.ID = &b
		}
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
