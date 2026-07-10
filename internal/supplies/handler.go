package supplies

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"faro/internal/auth"
)

// Routes se monta en /supplies. Lectura (catálogo/existencias/recetas) por sesión;
// escritura (catálogo, movimientos, recetas) solo super admin (patrón categories).
func (svc *Service) Routes(requireSession, requireSuperAdmin func(http.Handler) http.Handler) http.Handler {
	r := chi.NewRouter()

	// Lectura: cualquier usuario del negocio con sesión.
	r.Group(func(r chi.Router) {
		r.Use(requireSession)
		r.Get("/", svc.handleList)
		r.Get("/{id}", svc.handleGet)
		r.Get("/recipes/{productId}", svc.handleGetRecipe)
	})
	// Escritura: solo super admin.
	r.Group(func(r chi.Router) {
		r.Use(requireSuperAdmin)
		r.Post("/", svc.handleCreate)
		r.Patch("/{id}", svc.handleUpdate)
		r.Post("/{id}/movements", svc.handleCreateMovement)
		r.Get("/{id}/movements", svc.handleListMovements)
		r.Put("/recipes/{productId}", svc.handleReplaceRecipe)
	})
	return r
}

// ---- Catálogo --------------------------------------------------------------

type createRequest struct {
	Name             string `json:"name"`
	BaseUnit         string `json:"baseUnit"`
	PackageName      string `json:"packageName"`
	PackageContent   int    `json:"packageContent"`
	PackageCostCents *int   `json:"packageCostCents"` // opcional; null = costo no capturado
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
	sp, err := svc.Create(r.Context(), tenantID, req.Name, req.BaseUnit, req.PackageName, req.PackageContent, req.PackageCostCents)
	switch {
	case errors.Is(err, ErrValidation):
		writeError(w, http.StatusBadRequest, "validation_error", "Datos del insumo inválidos")
	case errors.Is(err, ErrNameTaken):
		writeError(w, http.StatusConflict, "name_taken", "Ya existe un insumo con ese nombre")
	case err != nil:
		writeError(w, http.StatusInternalServerError, "internal", "No se pudo crear el insumo")
	default:
		writeJSON(w, http.StatusCreated, map[string]any{"supply": sp})
	}
}

func (svc *Service) handleList(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := auth.ResolveTenant(w, r)
	if !ok {
		return
	}
	items, err := svc.List(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "No se pudieron listar los insumos")
		return
	}
	if items == nil {
		items = []Supply{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (svc *Service) handleGet(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := auth.ResolveTenant(w, r)
	if !ok {
		return
	}
	sp, err := svc.Get(r.Context(), tenantID, chi.URLParam(r, "id"))
	switch {
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "Insumo no encontrado")
	case err != nil:
		writeError(w, http.StatusInternalServerError, "internal", "No se pudo obtener el insumo")
	default:
		writeJSON(w, http.StatusOK, map[string]any{"supply": sp})
	}
}

type updateRequest struct {
	Name             *string `json:"name"`
	Status           *string `json:"status"`
	PackageName      *string `json:"packageName"`
	PackageContent   *int    `json:"packageContent"`
	PackageCostCents *int    `json:"packageCostCents"` // nil = no cambia
	BaseUnit         *string `json:"baseUnit"`         // inmutable: si viene => validation_error
}

func (svc *Service) handleUpdate(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := auth.ResolveTenant(w, r)
	if !ok {
		return
	}
	var req updateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", "Cuerpo inválido")
		return
	}
	sp, err := svc.Update(r.Context(), tenantID, chi.URLParam(r, "id"), UpdateInput{
		Name: req.Name, Status: req.Status, PackageName: req.PackageName,
		PackageContent: req.PackageContent, PackageCostCents: req.PackageCostCents,
		BaseUnit: req.BaseUnit,
	})
	switch {
	case errors.Is(err, ErrValidation):
		writeError(w, http.StatusBadRequest, "validation_error", "Datos inválidos (la unidad base no se puede cambiar)")
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "Insumo no encontrado")
	case errors.Is(err, ErrNameTaken):
		writeError(w, http.StatusConflict, "name_taken", "Ya existe un insumo con ese nombre")
	case err != nil:
		writeError(w, http.StatusInternalServerError, "internal", "No se pudo actualizar el insumo")
	default:
		writeJSON(w, http.StatusOK, map[string]any{"supply": sp})
	}
}

// ---- Movimientos -----------------------------------------------------------

type movementRequest struct {
	Type         string  `json:"type"`
	BranchID     string  `json:"branchId"`
	Packages     *int    `json:"packages"`
	QuantityBase *int    `json:"quantityBase"`
	Reason       *string `json:"reason"`
}

func (svc *Service) handleCreateMovement(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := auth.ResolveTenant(w, r)
	if !ok {
		return
	}
	u, ok := auth.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "Sesión requerida")
		return
	}
	var req movementRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", "Cuerpo inválido")
		return
	}
	m, stockBase, err := svc.CreateMovement(r.Context(), tenantID, chi.URLParam(r, "id"), MovementInput{
		Type: req.Type, BranchID: req.BranchID, Packages: req.Packages,
		QuantityBase: req.QuantityBase, Reason: req.Reason,
	}, u.ID)
	switch {
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "Insumo no encontrado")
	case errors.Is(err, ErrInvalidBranch):
		writeError(w, http.StatusBadRequest, "invalid_branch", "La sucursal no es válida")
	case errors.Is(err, ErrValidation):
		writeError(w, http.StatusBadRequest, "validation_error", "Datos del movimiento inválidos")
	case err != nil:
		writeError(w, http.StatusInternalServerError, "internal", "No se pudo registrar el movimiento")
	default:
		writeJSON(w, http.StatusCreated, map[string]any{"movement": m, "stockBase": stockBase})
	}
}

func (svc *Service) handleListMovements(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := auth.ResolveTenant(w, r)
	if !ok {
		return
	}
	from := parseTime(r.URL.Query().Get("from"))
	to := parseTime(r.URL.Query().Get("to"))
	items, err := svc.ListMovements(r.Context(), tenantID, chi.URLParam(r, "id"), from, to)
	switch {
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "Insumo no encontrado")
	case err != nil:
		writeError(w, http.StatusInternalServerError, "internal", "No se pudieron listar los movimientos")
	default:
		if items == nil {
			items = []Movement{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	}
}

// ---- Recetas ---------------------------------------------------------------

func (svc *Service) handleGetRecipe(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := auth.ResolveTenant(w, r)
	if !ok {
		return
	}
	items, err := svc.GetRecipe(r.Context(), tenantID, chi.URLParam(r, "productId"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "No se pudo obtener la receta")
		return
	}
	if items == nil {
		items = []RecipeItem{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

type recipeRequest struct {
	Items []struct {
		SupplyID     string `json:"supplyId"`
		QuantityBase int    `json:"quantityBase"`
	} `json:"items"`
}

func (svc *Service) handleReplaceRecipe(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := auth.ResolveTenant(w, r)
	if !ok {
		return
	}
	var req recipeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", "Cuerpo inválido")
		return
	}
	items := make([]recipeItemIn, 0, len(req.Items))
	for _, it := range req.Items {
		items = append(items, recipeItemIn{SupplyID: it.SupplyID, QuantityBase: it.QuantityBase})
	}
	out, err := svc.ReplaceRecipe(r.Context(), tenantID, chi.URLParam(r, "productId"), items)
	switch {
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "Producto no encontrado")
	case errors.Is(err, ErrInvalidSupply):
		writeError(w, http.StatusBadRequest, "invalid_supply", "Uno de los insumos no es válido")
	case errors.Is(err, ErrValidation):
		writeError(w, http.StatusBadRequest, "validation_error", "Receta inválida")
	case err != nil:
		writeError(w, http.StatusInternalServerError, "internal", "No se pudo guardar la receta")
	default:
		if out == nil {
			out = []RecipeItem{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": out})
	}
}

// parseTime acepta una fecha RFC3339 (la que envía el frontend); inválida => nil.
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
