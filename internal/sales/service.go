package sales

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrValidation = errors.New("validation")

// validPaymentMethods son las formas de pago aceptadas. "cash" cobra con cambio;
// el resto (card, transfer, didi) se cobran por el monto exacto, sin cambio.
var validPaymentMethods = map[string]bool{
	"cash":     true,
	"card":     true,
	"transfer": true,
	"didi":     true,
}

// isValidPaymentMethod indica si la forma de pago está permitida.
func isValidPaymentMethod(m string) bool { return validPaymentMethods[m] }

// isExactPaymentMethod indica si el método se cobra por el total exacto (sin
// cambio): todo lo que no sea efectivo. Fuente única para el cálculo del cambio.
func isExactPaymentMethod(m string) bool { return m != "cash" }

// LineInput es una línea solicitada por el cliente (producto + cantidad).
type LineInput struct {
	ProductID string
	Quantity  int
}

type Service struct {
	store *store
}

func NewService(pool *pgxpool.Pool) *Service {
	return &Service{store: newStore(pool)}
}

// Create valida la solicitud y registra la venta. El servidor calcula el total y
// el descuento de lealtad desde la promoción (no se confía en el cliente).
// branchID se deriva del usuario autenticado (nunca del cliente); nil = sin sucursal.
func (svc *Service) Create(ctx context.Context, tenantID string, items []LineInput, paymentMethod string, amountPaidCents int, customerID, promotionID, promotionProductID, branchID *string) (Sale, error) {
	if len(items) == 0 || amountPaidCents < 0 {
		return Sale{}, ErrValidation
	}
	if !isValidPaymentMethod(paymentMethod) {
		return Sale{}, ErrValidation
	}
	customerID = nilIfBlank(customerID)
	promotionID = nilIfBlank(promotionID)
	promotionProductID = nilIfBlank(promotionProductID)
	branchID = nilIfBlank(branchID)
	// La promoción solo tiene sentido con cliente asociado.
	if customerID == nil {
		promotionID = nil
		promotionProductID = nil
	}
	for _, it := range items {
		if strings.TrimSpace(it.ProductID) == "" || it.Quantity <= 0 {
			return Sale{}, ErrValidation
		}
	}
	return svc.store.createSale(ctx, tenantID, items, paymentMethod, amountPaidCents, customerID, promotionID, promotionProductID, branchID)
}

// nilIfBlank normaliza un puntero de string vacío/espacios a nil.
func nilIfBlank(p *string) *string {
	if p == nil {
		return nil
	}
	v := strings.TrimSpace(*p)
	if v == "" {
		return nil
	}
	return &v
}

// List devuelve las ventas del negocio acotadas a una sucursal (la activa de la
// sesión). branchID es obligatorio: el POS solo ve su propia sucursal.
func (svc *Service) List(ctx context.Context, tenantID, branchID string, from, to *time.Time) ([]Sale, error) {
	return svc.store.listByTenant(ctx, tenantID, branchID, from, to)
}

func (svc *Service) Get(ctx context.Context, tenantID, id string) (Sale, error) {
	return svc.store.get(ctx, tenantID, id)
}
