package sales

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrValidation = errors.New("validation")

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
func (svc *Service) Create(ctx context.Context, tenantID string, items []LineInput, paymentMethod string, amountPaidCents int, customerID, promotionID, promotionProductID *string) (Sale, error) {
	if len(items) == 0 || amountPaidCents < 0 {
		return Sale{}, ErrValidation
	}
	if paymentMethod != "cash" && paymentMethod != "card" {
		return Sale{}, ErrValidation
	}
	customerID = nilIfBlank(customerID)
	promotionID = nilIfBlank(promotionID)
	promotionProductID = nilIfBlank(promotionProductID)
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
	return svc.store.createSale(ctx, tenantID, items, paymentMethod, amountPaidCents, customerID, promotionID, promotionProductID)
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

func (svc *Service) List(ctx context.Context, tenantID string, from, to *time.Time) ([]Sale, error) {
	return svc.store.listByTenant(ctx, tenantID, from, to)
}

func (svc *Service) Get(ctx context.Context, tenantID, id string) (Sale, error) {
	return svc.store.get(ctx, tenantID, id)
}
