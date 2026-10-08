package requisitions

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Service orquesta la lógica de requisiciones de insumos. Tenant-scoped; el gating por rol
// lo aplican los handlers (inline).
type Service struct {
	store *store
}

func NewService(pool *pgxpool.Pool) *Service {
	return &Service{store: newStore(pool)}
}

// CreateRequisition valida (sucursal del negocio, al menos una línea, cantidades > 0,
// insumos del negocio sin duplicados) y crea la requisición en 'pending'. branchID = la
// sucursal activa del solicitante.
func (svc *Service) CreateRequisition(ctx context.Context, tenantID, branchID string, note *string, items []LineInput, requestedBy string) (RequisitionDetail, error) {
	branchID = strings.TrimSpace(branchID)
	if branchID == "" {
		return RequisitionDetail{}, ErrInvalidBranch
	}
	if len(items) == 0 {
		return RequisitionDetail{}, ErrValidation
	}
	okBranch, err := svc.store.branchInTenant(ctx, tenantID, branchID)
	if err != nil {
		return RequisitionDetail{}, err
	}
	if !okBranch {
		return RequisitionDetail{}, ErrInvalidBranch
	}

	seen := map[string]bool{}
	lines := make([]LineInput, 0, len(items))
	for _, it := range items {
		sid := strings.TrimSpace(it.SupplyID)
		if sid == "" || it.QuantityBase <= 0 {
			return RequisitionDetail{}, ErrValidation
		}
		if seen[sid] {
			return RequisitionDetail{}, ErrValidation
		}
		seen[sid] = true
		ok, err := svc.store.supplyInTenant(ctx, tenantID, sid)
		if err != nil {
			return RequisitionDetail{}, err
		}
		if !ok {
			return RequisitionDetail{}, ErrInvalidSupply
		}
		lines = append(lines, LineInput{SupplyID: sid, QuantityBase: it.QuantityBase, Note: normalizeNote(it.Note)})
	}
	return svc.store.createRequisition(ctx, tenantID, branchID, normalizeNote(note), lines, requestedBy)
}

func (svc *Service) ListRequisitions(ctx context.Context, tenantID string, branchID, status *string, q string) ([]Requisition, error) {
	if status != nil {
		s := strings.TrimSpace(*status)
		if s != "pending" && s != "partial" && s != "fulfilled" && s != "cancelled" {
			return nil, ErrValidation
		}
		status = &s
	}
	return svc.store.listRequisitions(ctx, tenantID, branchID, status, strings.TrimSpace(q))
}

func (svc *Service) GetRequisition(ctx context.Context, tenantID, id string) (RequisitionDetail, error) {
	return svc.store.getRequisition(ctx, tenantID, id)
}

// RequisitionBranch devuelve la sucursal dueña (para el gating de cancelación).
func (svc *Service) RequisitionBranch(ctx context.Context, tenantID, id string) (string, error) {
	return svc.store.requisitionBranch(ctx, tenantID, id)
}

func (svc *Service) CancelRequisition(ctx context.Context, tenantID, id string) (RequisitionDetail, error) {
	return svc.store.cancelRequisition(ctx, tenantID, id)
}

func (svc *Service) CloseRequisition(ctx context.Context, tenantID, id string) (RequisitionDetail, error) {
	return svc.store.closeRequisition(ctx, tenantID, id)
}

func (svc *Service) Suggestions(ctx context.Context, tenantID, branchID string) ([]Suggestion, error) {
	return svc.store.suggestions(ctx, tenantID, branchID)
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
