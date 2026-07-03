package sales

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"faro/internal/auth"
)

// Routes devuelve el router del POS (se monta en /sales).
func (svc *Service) Routes(requireSession func(http.Handler) http.Handler) http.Handler {
	r := chi.NewRouter()
	r.Use(requireSession)
	r.Post("/", svc.handleCreate)
	r.Get("/", svc.handleList)
	r.Get("/{id}", svc.handleGet)
	return r
}

func tenantOf(w http.ResponseWriter, r *http.Request) (string, bool) {
	u, ok := auth.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "No autenticado")
		return "", false
	}
	if u.TenantID == nil {
		writeError(w, http.StatusBadRequest, "tenant_required", "Esta operación requiere un negocio")
		return "", false
	}
	return *u.TenantID, true
}

type lineRequest struct {
	ProductID string `json:"productId"`
	Quantity  int    `json:"quantity"`
}

type createRequest struct {
	Items              []lineRequest `json:"items"`
	PaymentMethod      string        `json:"paymentMethod"` // cash | card
	AmountPaidCents    int           `json:"amountPaidCents"`
	CustomerID         *string       `json:"customerId"`
	PromotionID        *string       `json:"promotionId"`        // a lo sumo una promoción
	PromotionProductID *string       `json:"promotionProductId"` // unidad beneficiada (opcional)
}

func (svc *Service) handleCreate(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := tenantOf(w, r)
	if !ok {
		return
	}
	var req createRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", "Cuerpo inválido")
		return
	}
	items := make([]LineInput, 0, len(req.Items))
	for _, it := range req.Items {
		items = append(items, LineInput{ProductID: it.ProductID, Quantity: it.Quantity})
	}

	sale, err := svc.Create(r.Context(), tenantID, items, req.PaymentMethod, req.AmountPaidCents, req.CustomerID, req.PromotionID, req.PromotionProductID)
	switch {
	case errors.Is(err, ErrValidation):
		writeError(w, http.StatusBadRequest, "validation_error", "Venta inválida (items y cantidades > 0)")
	case errors.Is(err, ErrInvalidCustomer):
		writeError(w, http.StatusBadRequest, "invalid_customer", "El cliente no es válido")
	case errors.Is(err, ErrInvalidProduct):
		writeError(w, http.StatusBadRequest, "invalid_product", "Algún producto no es válido o está inactivo")
	case errors.Is(err, ErrPromotionNotEligible):
		writeError(w, http.StatusUnprocessableEntity, "promotion_not_eligible", "La promoción no es aplicable (umbral no alcanzado)")
	case errors.Is(err, ErrInsufficientPayment):
		writeError(w, http.StatusBadRequest, "insufficient_payment", "El monto recibido es menor al total")
	case err != nil:
		writeError(w, http.StatusInternalServerError, "internal", "No se pudo registrar la venta")
	default:
		writeJSON(w, http.StatusCreated, map[string]any{"sale": sale})
	}
}

func (svc *Service) handleList(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := tenantOf(w, r)
	if !ok {
		return
	}
	from := parseTime(r.URL.Query().Get("from"))
	to := parseTime(r.URL.Query().Get("to"))
	items, err := svc.List(r.Context(), tenantID, from, to)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "No se pudieron listar las ventas")
		return
	}
	if items == nil {
		items = []Sale{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
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

func (svc *Service) handleGet(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := tenantOf(w, r)
	if !ok {
		return
	}
	sale, err := svc.Get(r.Context(), tenantID, chi.URLParam(r, "id"))
	switch {
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "Venta no encontrada")
	case err != nil:
		writeError(w, http.StatusInternalServerError, "internal", "No se pudo obtener la venta")
	default:
		writeJSON(w, http.StatusOK, map[string]any{"sale": sale})
	}
}
