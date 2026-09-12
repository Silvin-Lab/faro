package expenses

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"faro/internal/auth"
)

// deleteWindow es la ventana en la que cashier/barista pueden borrar sus propios
// gastos. La UI solo muestra los de HOY; el guard server-side usa now()-24h como
// aproximación conservadora (no depende de la zona horaria del dispositivo). Ver
// plan Gastos §"Día del cajero".
const deleteWindow = 24 * time.Hour

// Routes se monta en /expenses. Catálogo: lectura por sesión, escritura solo super
// admin (patrón categories). Registro de gastos: cualquier rol de sucursal con
// sesión, con autorización fina inline por rol (patrón reports).
func (svc *Service) Routes(requireSession, requireSuperAdmin func(http.Handler) http.Handler) http.Handler {
	r := chi.NewRouter()

	// Catálogo: lectura (GET) para cualquier usuario del negocio.
	r.Group(func(r chi.Router) {
		r.Use(requireSession)
		r.Get("/categories", svc.handleListCategories)
		r.Get("/concepts", svc.handleListConcepts)
	})
	// Catálogo: escritura solo super admin.
	r.Group(func(r chi.Router) {
		r.Use(requireSuperAdmin)
		r.Post("/categories", svc.handleCreateCategory)
		r.Patch("/categories/{id}", svc.handleUpdateCategory)
		r.Post("/concepts", svc.handleCreateConcept)
		r.Patch("/concepts/{id}", svc.handleUpdateConcept)
	})
	// Registro de gastos: cualquier rol con sesión (authz fina en el handler).
	r.Group(func(r chi.Router) {
		r.Use(requireSession)
		r.Post("/", svc.handleCreateExpense)
		r.Get("/", svc.handleListExpenses)
		r.Delete("/{id}", svc.handleDeleteExpense)
	})
	return r
}

// activeBranch devuelve la sucursal activa de la sesión (claim del JWT). Escribe
// 400 branch_required y devuelve false si el usuario no tiene sucursal activa.
func activeBranch(w http.ResponseWriter, r *http.Request) (string, bool) {
	ab, _ := auth.ActiveBranchFromContext(r.Context())
	if ab == nil || *ab == "" {
		writeError(w, http.StatusBadRequest, "branch_required", "Debes seleccionar una sucursal")
		return "", false
	}
	return *ab, true
}

// ---- Categorías ------------------------------------------------------------

type categoryRequest struct {
	Name      string `json:"name"`
	SortOrder int    `json:"sortOrder"`
}

func (svc *Service) handleCreateCategory(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := auth.ResolveTenant(w, r)
	if !ok {
		return
	}
	var req categoryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", "Cuerpo inválido")
		return
	}
	c, err := svc.CreateCategory(r.Context(), tenantID, req.Name, req.SortOrder)
	switch {
	case errors.Is(err, ErrValidation):
		writeError(w, http.StatusBadRequest, "validation_error", "El nombre es requerido")
	case errors.Is(err, ErrNameTaken):
		writeError(w, http.StatusConflict, "name_taken", "Ya existe una categoría con ese nombre")
	case err != nil:
		writeError(w, http.StatusInternalServerError, "internal", "No se pudo crear la categoría")
	default:
		writeJSON(w, http.StatusCreated, map[string]any{"category": c})
	}
}

func (svc *Service) handleListCategories(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := auth.ResolveTenant(w, r)
	if !ok {
		return
	}
	items, err := svc.ListCategories(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "No se pudieron listar las categorías")
		return
	}
	if items == nil {
		items = []Category{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

type categoryUpdateRequest struct {
	Name      *string `json:"name"`
	Status    *string `json:"status"`
	SortOrder *int    `json:"sortOrder"`
}

func (svc *Service) handleUpdateCategory(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := auth.ResolveTenant(w, r)
	if !ok {
		return
	}
	var req categoryUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", "Cuerpo inválido")
		return
	}
	c, err := svc.UpdateCategory(r.Context(), tenantID, chi.URLParam(r, "id"),
		CategoryUpdate{Name: req.Name, Status: req.Status, SortOrder: req.SortOrder})
	switch {
	case errors.Is(err, ErrValidation):
		writeError(w, http.StatusBadRequest, "validation_error", "Datos inválidos")
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "Categoría no encontrada")
	case errors.Is(err, ErrNameTaken):
		writeError(w, http.StatusConflict, "name_taken", "Ya existe una categoría con ese nombre")
	case err != nil:
		writeError(w, http.StatusInternalServerError, "internal", "No se pudo actualizar la categoría")
	default:
		writeJSON(w, http.StatusOK, map[string]any{"category": c})
	}
}

// ---- Conceptos -------------------------------------------------------------

type conceptRequest struct {
	Name       string  `json:"name"`
	CategoryID *string `json:"categoryId"`
}

func (svc *Service) handleCreateConcept(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := auth.ResolveTenant(w, r)
	if !ok {
		return
	}
	var req conceptRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", "Cuerpo inválido")
		return
	}
	c, err := svc.CreateConcept(r.Context(), tenantID, req.CategoryID, req.Name)
	switch {
	case errors.Is(err, ErrValidation):
		writeError(w, http.StatusBadRequest, "validation_error", "El nombre es requerido")
	case errors.Is(err, ErrInvalidCategory):
		writeError(w, http.StatusBadRequest, "invalid_category", "La categoría no es válida")
	case errors.Is(err, ErrNameTaken):
		writeError(w, http.StatusConflict, "name_taken", "Ya existe un concepto con ese nombre")
	case err != nil:
		writeError(w, http.StatusInternalServerError, "internal", "No se pudo crear el concepto")
	default:
		writeJSON(w, http.StatusCreated, map[string]any{"concept": c})
	}
}

func (svc *Service) handleListConcepts(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := auth.ResolveTenant(w, r)
	if !ok {
		return
	}
	items, err := svc.ListConcepts(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "No se pudieron listar los conceptos")
		return
	}
	if items == nil {
		items = []Concept{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

type conceptUpdateRequest struct {
	Name       *string `json:"name"`
	Status     *string `json:"status"`
	CategoryID *string `json:"categoryId"`
}

func (svc *Service) handleUpdateConcept(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := auth.ResolveTenant(w, r)
	if !ok {
		return
	}
	var req conceptUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", "Cuerpo inválido")
		return
	}
	c, err := svc.UpdateConcept(r.Context(), tenantID, chi.URLParam(r, "id"),
		ConceptUpdate{Name: req.Name, Status: req.Status, CategoryID: req.CategoryID})
	switch {
	case errors.Is(err, ErrValidation):
		writeError(w, http.StatusBadRequest, "validation_error", "Datos inválidos")
	case errors.Is(err, ErrInvalidCategory):
		writeError(w, http.StatusBadRequest, "invalid_category", "La categoría no es válida")
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "Concepto no encontrado")
	case errors.Is(err, ErrNameTaken):
		writeError(w, http.StatusConflict, "name_taken", "Ya existe un concepto con ese nombre")
	case err != nil:
		writeError(w, http.StatusInternalServerError, "internal", "No se pudo actualizar el concepto")
	default:
		writeJSON(w, http.StatusOK, map[string]any{"concept": c})
	}
}

// ---- Gastos ----------------------------------------------------------------

type createExpenseRequest struct {
	ConceptID   string  `json:"conceptId"`
	AmountCents int     `json:"amountCents"`
	BranchID    *string `json:"branchId"`
}

func (svc *Service) handleCreateExpense(w http.ResponseWriter, r *http.Request) {
	// ResolveTenant (no TenantOf) para que la administración central (super_admin, sin
	// tenant propio) resuelva el tenant del negocio único y pueda registrar gastos.
	tenantID, ok := auth.ResolveTenant(w, r)
	if !ok {
		return
	}
	u, ok := auth.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "Sesión requerida")
		return
	}
	var req createExpenseRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", "Cuerpo inválido")
		return
	}

	// Resolución del branch a registrar según rol:
	//   super_admin: usa req.BranchID tal cual (nil/ausente = gasto "General" sin
	//     sucursal; con valor = esa sucursal, que el service valida del tenant).
	//   usuario de sucursal: SIEMPRE su sucursal activa; el req.BranchID del body se
	//     ignora (no puede inyectar una sucursal distinta a la suya).
	var branchID *string
	if u.IsSuperAdmin {
		branchID = req.BranchID
	} else {
		active, okB := activeBranch(w, r)
		if !okB {
			return
		}
		branchID = &active
	}

	e, err := svc.CreateExpense(r.Context(), tenantID, branchID, req.ConceptID, req.AmountCents, u.ID)
	switch {
	case errors.Is(err, ErrValidation):
		writeError(w, http.StatusBadRequest, "validation_error", "El monto debe ser mayor a cero")
	case errors.Is(err, ErrInvalidConcept):
		writeError(w, http.StatusBadRequest, "invalid_concept", "El concepto no es válido o está inactivo")
	case errors.Is(err, ErrInvalidBranch):
		writeError(w, http.StatusBadRequest, "invalid_branch", "La sucursal no es válida")
	case err != nil:
		writeError(w, http.StatusInternalServerError, "internal", "No se pudo registrar el gasto")
	default:
		writeJSON(w, http.StatusCreated, map[string]any{"expense": e})
	}
}

func (svc *Service) handleListExpenses(w http.ResponseWriter, r *http.Request) {
	u, ok := auth.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "Sesión requerida")
		return
	}
	tenantID, ok := auth.ResolveTenant(w, r)
	if !ok {
		return
	}

	// Filtro por sucursal: los roles de sucursal quedan forzados a su sucursal
	// activa (se ignora ?branchId); el super admin puede acotar con ?branchId.
	var branchID *string
	if u.IsSuperAdmin {
		if b := r.URL.Query().Get("branchId"); b != "" {
			branchID = &b
		}
	} else {
		active, okB := activeBranch(w, r)
		if !okB {
			return
		}
		branchID = &active
	}

	from := parseTime(r.URL.Query().Get("from"))
	to := parseTime(r.URL.Query().Get("to"))
	items, err := svc.ListExpenses(r.Context(), tenantID, branchID, from, to)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "No se pudieron listar los gastos")
		return
	}
	if items == nil {
		items = []Expense{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (svc *Service) handleDeleteExpense(w http.ResponseWriter, r *http.Request) {
	u, ok := auth.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "Sesión requerida")
		return
	}
	tenantID, ok := auth.ResolveTenant(w, r)
	if !ok {
		return
	}
	id := chi.URLParam(r, "id")

	branchID, createdAt, err := svc.ExpenseForDelete(r.Context(), tenantID, id)
	switch {
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "Gasto no encontrado")
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, "internal", "No se pudo borrar el gasto")
		return
	}

	// Matriz de autorización por rol (403 si no aplica; el gasto SÍ existe -> nunca
	// se filtra como 404). super_admin: cualquiera del tenant. branch_admin:
	// cualquiera de su sucursal activa. cashier/barista: solo de su sucursal activa
	// y dentro de la ventana de 24h.
	if !u.IsSuperAdmin {
		active, _ := auth.ActiveBranchFromContext(r.Context())
		if active == nil || *active == "" {
			writeError(w, http.StatusBadRequest, "branch_required", "Debes seleccionar una sucursal")
			return
		}
		// branchID nil = gasto "General" (sin sucursal): nunca coincide con la sucursal
		// activa, así que solo el super_admin puede borrarlo (igual que un gasto de otra
		// sucursal).
		if branchID == nil || *branchID != *active {
			writeError(w, http.StatusForbidden, "forbidden", "No puedes borrar gastos de otra sucursal")
			return
		}
		if u.Role != auth.RoleBranchAdmin {
			// cashier/barista: además, solo dentro de la ventana de borrado.
			if time.Since(createdAt) > deleteWindow {
				writeError(w, http.StatusForbidden, "forbidden", "Solo puedes borrar gastos recientes")
				return
			}
		}
	}

	if err := svc.DeleteExpense(r.Context(), tenantID, id); err != nil {
		if errors.Is(err, ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "Gasto no encontrado")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal", "No se pudo borrar el gasto")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
}

// parseTime acepta una fecha RFC3339 (la que envía el frontend); inválida -> nil.
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
