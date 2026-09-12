package bakery

import (
	"context"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Service orquesta la lógica de repostería: pedidos, producción (doble efecto de stock),
// stock de postre y auditoría. Tenant-scoped; el gating por rol lo aplican los handlers
// (inline). Todas las mutaciones de stock viven en transacciones del store.
type Service struct {
	store *store
}

func NewService(pool *pgxpool.Pool) *Service {
	return &Service{store: newStore(pool)}
}

// CreateOrder valida (cantidad > 0, sucursal del negocio, producto activo de repostería) y
// crea un pedido en 'pending'. branchID = sucursal activa del solicitante (no del cliente).
func (svc *Service) CreateOrder(ctx context.Context, tenantID, branchID, productID string, quantity int, note *string, requestedBy string) (Order, error) {
	branchID = strings.TrimSpace(branchID)
	productID = strings.TrimSpace(productID)
	if quantity <= 0 {
		return Order{}, ErrValidation
	}
	if branchID == "" {
		return Order{}, ErrInvalidBranch
	}
	okBranch, err := svc.store.branchInTenant(ctx, tenantID, branchID)
	if err != nil {
		return Order{}, err
	}
	if !okBranch {
		return Order{}, ErrInvalidBranch
	}
	okProduct, err := svc.store.productBakeryActive(ctx, tenantID, productID)
	if err != nil {
		return Order{}, err
	}
	if !okProduct {
		return Order{}, ErrInvalidProduct
	}
	return svc.store.createOrder(ctx, tenantID, branchID, productID, quantity, normalizeNote(note), requestedBy)
}

func (svc *Service) ListOrders(ctx context.Context, tenantID string, branchID *string, statuses []string, q string) ([]Order, error) {
	return svc.store.listOrders(ctx, tenantID, branchID, statuses, strings.TrimSpace(q))
}

func (svc *Service) GetOrder(ctx context.Context, tenantID, id string) (Order, error) {
	return svc.store.getOrder(ctx, tenantID, id)
}

func (svc *Service) ProductionsForOrder(ctx context.Context, tenantID, orderID string) ([]Production, error) {
	return svc.store.productionsForOrder(ctx, tenantID, orderID)
}

// Produce valida cantidad > 0 y ejecuta la transacción de producción (§4).
func (svc *Service) Produce(ctx context.Context, tenantID, orderID string, quantity int, createdBy string) (Order, Production, error) {
	if quantity <= 0 {
		return Order{}, Production{}, ErrValidation
	}
	return svc.store.produce(ctx, tenantID, orderID, quantity, createdBy)
}

func (svc *Service) CancelOrder(ctx context.Context, tenantID, id string) (Order, error) {
	return svc.store.cancelOrder(ctx, tenantID, id)
}

func (svc *Service) ReceiveOrder(ctx context.Context, tenantID, id string) (Order, error) {
	return svc.store.receiveOrder(ctx, tenantID, id)
}

func (svc *Service) ListStock(ctx context.Context, tenantID string, branchID *string, q string) ([]StockItem, error) {
	return svc.store.listStock(ctx, tenantID, branchID, strings.TrimSpace(q))
}

func (svc *Service) ListProductions(ctx context.Context, tenantID string, from, to *time.Time, branchID, productID *string) ([]ProductionAudit, error) {
	return svc.store.listProductions(ctx, tenantID, from, to, branchID, productID)
}

// normalizeNote recorta la nota; vacía => nil.
func normalizeNote(note *string) *string {
	if note == nil {
		return nil
	}
	v := strings.TrimSpace(*note)
	if v == "" {
		return nil
	}
	return &v
}
