package warehouse

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"faro/internal/auth"
)

// Routes se monta en /warehouse. TODO el módulo (lectura y escritura) exige
// super_admin: el router pasa requireSuperAdmin y se aplica a todas las rutas
// (tech-spec §1, handoff §1.2).
func (svc *Service) Routes(requireSuperAdmin func(http.Handler) http.Handler) http.Handler {
	r := chi.NewRouter()
	r.Use(requireSuperAdmin)

	// Existencias + mín/máx + a-comprar.
	r.Get("/stock", svc.handleListStock)
	r.Patch("/stock/{supplyId}", svc.handleUpdateMinMax)
	r.Get("/to-buy", svc.handleToBuy)

	// Proveedores.
	r.Get("/suppliers", svc.handleListSuppliers)
	r.Post("/suppliers", svc.handleCreateSupplier)
	r.Patch("/suppliers/{id}", svc.handleUpdateSupplier)

	// Compras.
	r.Post("/purchases", svc.handleCreatePurchase)
	r.Get("/purchases", svc.handleListPurchases)

	// Salidas.
	r.Post("/dispatches", svc.handleCreateDispatch)
	r.Get("/dispatches", svc.handleListDispatches)

	// Mermas.
	r.Post("/waste", svc.handleCreateWaste)
	r.Get("/waste", svc.handleListWaste)

	return r
}

// ---- Proveedores -----------------------------------------------------------

type supplierRequest struct {
	Name    string  `json:"name"`
	Address *string `json:"address"`
	Email   *string `json:"email"`
	Phone   *string `json:"phone"`
}

func (svc *Service) handleCreateSupplier(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := auth.ResolveTenant(w, r)
	if !ok {
		return
	}
	var req supplierRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", "Cuerpo inválido")
		return
	}
	sp, err := svc.CreateSupplier(r.Context(), tenantID, SupplierInput{
		Name: req.Name, Address: req.Address, Email: req.Email, Phone: req.Phone,
	})
	switch {
	case errors.Is(err, ErrValidation):
		writeError(w, http.StatusBadRequest, "validation_error", "El nombre es requerido")
	case errors.Is(err, ErrNameTaken):
		writeError(w, http.StatusConflict, "name_taken", "Ya existe un proveedor con ese nombre")
	case err != nil:
		writeError(w, http.StatusInternalServerError, "internal", "No se pudo crear el proveedor")
	default:
		writeJSON(w, http.StatusCreated, map[string]any{"supplier": sp})
	}
}

func (svc *Service) handleListSuppliers(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := auth.ResolveTenant(w, r)
	if !ok {
		return
	}
	var status *string
	if s := r.URL.Query().Get("status"); s != "" {
		status = &s
	}
	items, err := svc.ListSuppliers(r.Context(), tenantID, status)
	switch {
	case errors.Is(err, ErrValidation):
		writeError(w, http.StatusBadRequest, "validation_error", "status inválido")
	case err != nil:
		writeError(w, http.StatusInternalServerError, "internal", "No se pudieron listar los proveedores")
	default:
		if items == nil {
			items = []Supplier{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	}
}

type supplierUpdateRequest struct {
	Name    *string `json:"name"`
	Address *string `json:"address"`
	Email   *string `json:"email"`
	Phone   *string `json:"phone"`
	Status  *string `json:"status"`
}

func (svc *Service) handleUpdateSupplier(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := auth.ResolveTenant(w, r)
	if !ok {
		return
	}
	var req supplierUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", "Cuerpo inválido")
		return
	}
	sp, err := svc.UpdateSupplier(r.Context(), tenantID, chi.URLParam(r, "id"), SupplierUpdate{
		Name: req.Name, Address: req.Address, Email: req.Email, Phone: req.Phone, Status: req.Status,
	})
	switch {
	case errors.Is(err, ErrValidation):
		writeError(w, http.StatusBadRequest, "validation_error", "Datos inválidos")
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "Proveedor no encontrado")
	case errors.Is(err, ErrNameTaken):
		writeError(w, http.StatusConflict, "name_taken", "Ya existe un proveedor con ese nombre")
	case err != nil:
		writeError(w, http.StatusInternalServerError, "internal", "No se pudo actualizar el proveedor")
	default:
		writeJSON(w, http.StatusOK, map[string]any{"supplier": sp})
	}
}

// ---- Existencias + mín/máx -------------------------------------------------

func (svc *Service) handleListStock(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := auth.ResolveTenant(w, r)
	if !ok {
		return
	}
	items, err := svc.ListStock(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "No se pudieron listar las existencias")
		return
	}
	if items == nil {
		items = []WarehouseStockItem{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (svc *Service) handleUpdateMinMax(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := auth.ResolveTenant(w, r)
	if !ok {
		return
	}
	// Presencia por campo: null y ausente se distinguen decodificando a RawMessage
	// (un campo presente con null limpia el límite; ausente lo deja intacto).
	var raw map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", "Cuerpo inválido")
		return
	}
	in := MinMaxUpdate{}
	if v, present := raw["minQuantity"]; present {
		in.SetMin = true
		if p, err := parseNullableInt(v); err != nil {
			writeError(w, http.StatusBadRequest, "validation_error", "minQuantity inválido")
			return
		} else {
			in.Min = p
		}
	}
	if v, present := raw["maxQuantity"]; present {
		in.SetMax = true
		if p, err := parseNullableInt(v); err != nil {
			writeError(w, http.StatusBadRequest, "validation_error", "maxQuantity inválido")
			return
		} else {
			in.Max = p
		}
	}
	it, err := svc.UpdateMinMax(r.Context(), tenantID, chi.URLParam(r, "supplyId"), in)
	switch {
	case errors.Is(err, ErrValidation):
		writeError(w, http.StatusBadRequest, "validation_error", "El máximo no puede ser menor que el mínimo")
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "Insumo no encontrado")
	case err != nil:
		writeError(w, http.StatusInternalServerError, "internal", "No se pudieron guardar los límites")
	default:
		writeJSON(w, http.StatusOK, map[string]any{"item": it})
	}
}

func (svc *Service) handleToBuy(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := auth.ResolveTenant(w, r)
	if !ok {
		return
	}
	items, err := svc.ToBuy(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "No se pudieron listar los insumos a comprar")
		return
	}
	if items == nil {
		items = []ToBuyItem{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// ---- Compras ---------------------------------------------------------------

type purchaseRequest struct {
	SupplyID      string `json:"supplyId"`
	SupplierID    string `json:"supplierId"`
	Packages      int    `json:"packages"`
	UnitCostCents int    `json:"unitCostCents"`
	Date          string `json:"date"`
}

func (svc *Service) handleCreatePurchase(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := auth.ResolveTenant(w, r)
	if !ok {
		return
	}
	u, ok := auth.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "Sesión requerida")
		return
	}
	var req purchaseRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", "Cuerpo inválido")
		return
	}
	m, stockBase, err := svc.CreatePurchase(r.Context(), tenantID, PurchaseInput{
		SupplyID: req.SupplyID, SupplierID: req.SupplierID, Packages: req.Packages,
		UnitCostCents: req.UnitCostCents, Date: req.Date,
	}, u.ID)
	switch {
	case errors.Is(err, ErrValidation):
		writeError(w, http.StatusBadRequest, "validation_error", "Datos de la compra inválidos")
	case errors.Is(err, ErrInvalidSupply):
		writeError(w, http.StatusBadRequest, "invalid_supply", "El insumo no es válido")
	case errors.Is(err, ErrInvalidSupplier):
		writeError(w, http.StatusBadRequest, "invalid_supplier", "El proveedor no es válido")
	case err != nil:
		writeError(w, http.StatusInternalServerError, "internal", "No se pudo registrar la compra")
	default:
		writeJSON(w, http.StatusCreated, map[string]any{"movement": m, "stockBase": stockBase})
	}
}

func (svc *Service) handleListPurchases(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := auth.ResolveTenant(w, r)
	if !ok {
		return
	}
	from := parseTime(r.URL.Query().Get("from"))
	to := parseTime(r.URL.Query().Get("to"))
	items, err := svc.ListPurchases(r.Context(), tenantID, from, to)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "No se pudieron listar las compras")
		return
	}
	if items == nil {
		items = []PurchaseItem{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// ---- Salidas ---------------------------------------------------------------

type dispatchRequest struct {
	SupplyID     string `json:"supplyId"`
	BranchID     string `json:"branchId"`
	QuantityBase int    `json:"quantityBase"`
	Date         string `json:"date"`
}

func (svc *Service) handleCreateDispatch(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := auth.ResolveTenant(w, r)
	if !ok {
		return
	}
	u, ok := auth.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "Sesión requerida")
		return
	}
	var req dispatchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", "Cuerpo inválido")
		return
	}
	m, whStock, branchStock, err := svc.CreateDispatch(r.Context(), tenantID, DispatchInput{
		SupplyID: req.SupplyID, BranchID: req.BranchID, QuantityBase: req.QuantityBase, Date: req.Date,
	}, u.ID)
	switch {
	case errors.Is(err, ErrValidation):
		writeError(w, http.StatusBadRequest, "validation_error", "Datos de la salida inválidos")
	case errors.Is(err, ErrInvalidSupply):
		writeError(w, http.StatusBadRequest, "invalid_supply", "El insumo no es válido")
	case errors.Is(err, ErrInvalidBranch):
		writeError(w, http.StatusBadRequest, "invalid_branch", "La sucursal no es válida")
	case err != nil:
		writeError(w, http.StatusInternalServerError, "internal", "No se pudo registrar la salida")
	default:
		writeJSON(w, http.StatusCreated, map[string]any{
			"movement": m, "warehouseStockBase": whStock, "branchStockBase": branchStock,
		})
	}
}

func (svc *Service) handleListDispatches(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := auth.ResolveTenant(w, r)
	if !ok {
		return
	}
	from := parseTime(r.URL.Query().Get("from"))
	to := parseTime(r.URL.Query().Get("to"))
	items, err := svc.ListDispatches(r.Context(), tenantID, from, to)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "No se pudieron listar las salidas")
		return
	}
	if items == nil {
		items = []DispatchItem{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// ---- Mermas ----------------------------------------------------------------

type wasteRequest struct {
	SupplyID     string  `json:"supplyId"`
	QuantityBase int     `json:"quantityBase"`
	Reason       string  `json:"reason"`
	BranchID     *string `json:"branchId"`
	Date         string  `json:"date"`
}

func (svc *Service) handleCreateWaste(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := auth.ResolveTenant(w, r)
	if !ok {
		return
	}
	u, ok := auth.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "Sesión requerida")
		return
	}
	var req wasteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", "Cuerpo inválido")
		return
	}
	m, stockBase, err := svc.CreateWaste(r.Context(), tenantID, WasteInput{
		SupplyID: req.SupplyID, QuantityBase: req.QuantityBase, Reason: req.Reason,
		BranchID: req.BranchID, Date: req.Date,
	}, u.ID)
	switch {
	case errors.Is(err, ErrValidation):
		writeError(w, http.StatusBadRequest, "validation_error", "El motivo y una cantidad válida son requeridos")
	case errors.Is(err, ErrInvalidSupply):
		writeError(w, http.StatusBadRequest, "invalid_supply", "El insumo no es válido")
	case errors.Is(err, ErrInvalidBranch):
		writeError(w, http.StatusBadRequest, "invalid_branch", "La sucursal no es válida")
	case err != nil:
		writeError(w, http.StatusInternalServerError, "internal", "No se pudo registrar la merma")
	default:
		writeJSON(w, http.StatusCreated, map[string]any{"movement": m, "stockBase": stockBase})
	}
}

func (svc *Service) handleListWaste(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := auth.ResolveTenant(w, r)
	if !ok {
		return
	}
	from := parseTime(r.URL.Query().Get("from"))
	to := parseTime(r.URL.Query().Get("to"))
	items, err := svc.ListWaste(r.Context(), tenantID, from, to)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "No se pudieron listar las mermas")
		return
	}
	if items == nil {
		items = []WasteItem{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// ---- Helpers ---------------------------------------------------------------

// parseNullableInt decodifica un RawMessage a *int: JSON null => nil; número entero
// => *int; cualquier otra cosa => error.
func parseNullableInt(raw json.RawMessage) (*int, error) {
	if string(raw) == "null" {
		return nil, nil
	}
	var n int
	if err := json.Unmarshal(raw, &n); err != nil {
		return nil, err
	}
	return &n, nil
}

// parseTime acepta una fecha RFC3339; inválida/ausente => nil.
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
