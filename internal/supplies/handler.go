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

	// Lectura: cualquier usuario del negocio con sesión. Las rutas de segmento
	// estático (/categories, /recipes) se registran ANTES/junto a /{id} para que chi
	// las priorice sobre el path param: GET /supplies/categories NO cae en handleGet.
	r.Group(func(r chi.Router) {
		r.Use(requireSession)
		r.Get("/", svc.handleList)
		r.Get("/categories", svc.handleListCategories)
		r.Get("/recipes/{productId}", svc.handleGetRecipe)
		r.Get("/{id}/measures", svc.handleListMeasures)
		r.Get("/{id}", svc.handleGet)
	})
	// Escritura: solo super admin. Las rutas con segmento estático /measures/{measureId}
	// (medida por id, sin insumo en el path) se registran junto a /{id}/measures (medidas
	// de un insumo); chi prioriza el literal "measures" sobre el path param {id}.
	r.Group(func(r chi.Router) {
		r.Use(requireSuperAdmin)
		r.Post("/", svc.handleCreate)
		r.Post("/categories", svc.handleCreateCategory)
		r.Patch("/categories/{id}", svc.handleUpdateCategory)
		r.Post("/{id}/measures", svc.handleCreateMeasure)
		r.Patch("/measures/{measureId}", svc.handleUpdateMeasure)
		r.Delete("/measures/{measureId}", svc.handleDeleteMeasure)
		r.Patch("/{id}", svc.handleUpdate)
		r.Delete("/{id}", svc.handleDelete)
		r.Post("/{id}/movements", svc.handleCreateMovement)
		r.Get("/{id}/movements", svc.handleListMovements)
		r.Put("/recipes/{productId}", svc.handleReplaceRecipe)
	})
	return r
}

// ---- Categorías de insumo --------------------------------------------------

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
		items = []SupplyCategory{}
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

// ---- Catálogo --------------------------------------------------------------

type createRequest struct {
	Name             string  `json:"name"`
	BaseUnit         string  `json:"baseUnit"`
	PackageName      string  `json:"packageName"`
	PackageContent   int     `json:"packageContent"`
	PackageCostCents *int    `json:"packageCostCents"` // opcional; null = costo no capturado
	CategoryID       *string `json:"categoryId"`       // opcional; null/"" = sin categoría
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
	sp, err := svc.Create(r.Context(), tenantID, req.Name, req.BaseUnit, req.PackageName, req.PackageContent, req.PackageCostCents, req.CategoryID)
	switch {
	case errors.Is(err, ErrValidation):
		writeError(w, http.StatusBadRequest, "validation_error", "Datos del insumo inválidos")
	case errors.Is(err, ErrInvalidCategory):
		writeError(w, http.StatusBadRequest, "invalid_category", "La categoría no es válida")
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
	// status opcional: ?status=active para los selects de "nuevo insumo" (excluye los
	// dados de baja); ausente = todos (catálogo completo, incluye inactivos).
	var status *string
	if s := r.URL.Query().Get("status"); s != "" {
		status = &s
	}
	items, err := svc.List(r.Context(), tenantID, status)
	switch {
	case errors.Is(err, ErrValidation):
		writeError(w, http.StatusBadRequest, "validation_error", "status inválido")
	case err != nil:
		writeError(w, http.StatusInternalServerError, "internal", "No se pudieron listar los insumos")
	default:
		if items == nil {
			items = []Supply{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	}
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
	CategoryID       *string `json:"categoryId"`       // presente => asigna; ausente = no cambia
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
		BaseUnit: req.BaseUnit, CategoryID: req.CategoryID,
	})
	switch {
	case errors.Is(err, ErrValidation):
		writeError(w, http.StatusBadRequest, "validation_error", "Datos inválidos (la unidad base no se puede cambiar)")
	case errors.Is(err, ErrInvalidCategory):
		writeError(w, http.StatusBadRequest, "invalid_category", "La categoría no es válida")
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

// handleDelete da de baja un insumo (soft-delete: DELETE /supplies/{id} => status
// 'inactive'). No borra datos: el insumo se conserva en historiales y recetas
// existentes, solo deja de ofrecerse en los selects activos. 204 No Content.
func (svc *Service) handleDelete(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := auth.ResolveTenant(w, r)
	if !ok {
		return
	}
	err := svc.SoftDelete(r.Context(), tenantID, chi.URLParam(r, "id"))
	switch {
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "Insumo no encontrado")
	case err != nil:
		writeError(w, http.StatusInternalServerError, "internal", "No se pudo eliminar el insumo")
	default:
		w.WriteHeader(http.StatusNoContent)
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

// ---- Medidas de uso --------------------------------------------------------

func (svc *Service) handleListMeasures(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := auth.ResolveTenant(w, r)
	if !ok {
		return
	}
	items, err := svc.ListMeasures(r.Context(), tenantID, chi.URLParam(r, "id"))
	switch {
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "Insumo no encontrado")
	case err != nil:
		writeError(w, http.StatusInternalServerError, "internal", "No se pudieron listar las medidas")
	default:
		if items == nil {
			items = []SupplyMeasure{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	}
}

type measureRequest struct {
	Name         string `json:"name"`
	BaseQuantity int    `json:"baseQuantity"`
}

func (svc *Service) handleCreateMeasure(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := auth.ResolveTenant(w, r)
	if !ok {
		return
	}
	var req measureRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", "Cuerpo inválido")
		return
	}
	m, err := svc.CreateMeasure(r.Context(), tenantID, chi.URLParam(r, "id"), req.Name, req.BaseQuantity)
	switch {
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "Insumo no encontrado")
	case errors.Is(err, ErrValidation):
		writeError(w, http.StatusBadRequest, "validation_error", "El nombre y la equivalencia son requeridos")
	case errors.Is(err, ErrNameTaken):
		writeError(w, http.StatusConflict, "name_taken", "Ya existe una medida con ese nombre")
	case err != nil:
		writeError(w, http.StatusInternalServerError, "internal", "No se pudo crear la medida")
	default:
		writeJSON(w, http.StatusCreated, map[string]any{"measure": m})
	}
}

type measureUpdateRequest struct {
	Name         *string `json:"name"`
	BaseQuantity *int    `json:"baseQuantity"`
}

func (svc *Service) handleUpdateMeasure(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := auth.ResolveTenant(w, r)
	if !ok {
		return
	}
	var req measureUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", "Cuerpo inválido")
		return
	}
	m, err := svc.UpdateMeasure(r.Context(), tenantID, chi.URLParam(r, "measureId"),
		MeasureUpdate{Name: req.Name, BaseQuantity: req.BaseQuantity})
	switch {
	case errors.Is(err, ErrValidation):
		writeError(w, http.StatusBadRequest, "validation_error", "Datos inválidos")
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "Medida no encontrada")
	case errors.Is(err, ErrNameTaken):
		writeError(w, http.StatusConflict, "name_taken", "Ya existe una medida con ese nombre")
	case err != nil:
		writeError(w, http.StatusInternalServerError, "internal", "No se pudo actualizar la medida")
	default:
		writeJSON(w, http.StatusOK, map[string]any{"measure": m})
	}
}

func (svc *Service) handleDeleteMeasure(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := auth.ResolveTenant(w, r)
	if !ok {
		return
	}
	err := svc.DeleteMeasure(r.Context(), tenantID, chi.URLParam(r, "measureId"))
	switch {
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "Medida no encontrada")
	case err != nil:
		writeError(w, http.StatusInternalServerError, "internal", "No se pudo borrar la medida")
	default:
		w.WriteHeader(http.StatusNoContent)
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
		SupplyID     string   `json:"supplyId"`
		QuantityBase int      `json:"quantityBase"`
		MeasureID    *string  `json:"measureId"`    // opcional: línea capturada por medida
		MeasureCount *float64 `json:"measureCount"` // opcional: cantidad de medidas
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
		items = append(items, recipeItemIn{
			SupplyID: it.SupplyID, QuantityBase: it.QuantityBase,
			MeasureID: it.MeasureID, MeasureCount: it.MeasureCount,
		})
	}
	out, err := svc.ReplaceRecipe(r.Context(), tenantID, chi.URLParam(r, "productId"), items)
	switch {
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "Producto no encontrado")
	case errors.Is(err, ErrInvalidSupply):
		writeError(w, http.StatusBadRequest, "invalid_supply", "Uno de los insumos no es válido")
	case errors.Is(err, ErrInvalidMeasure):
		writeError(w, http.StatusBadRequest, "invalid_measure", "Una de las medidas no es válida")
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
