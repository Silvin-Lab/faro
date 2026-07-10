package reports

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Service struct {
	store *store
}

func NewService(pool *pgxpool.Pool) *Service {
	return &Service{store: newStore(pool)}
}

func (svc *Service) SalesReport(ctx context.Context, tenantID string, from, to time.Time, tzMinutes int, branch BranchFilter) (SalesReport, error) {
	return svc.store.salesReport(ctx, tenantID, from, to, tzMinutes, branch)
}

func (svc *Service) ExpensesReport(ctx context.Context, tenantID string, from, to time.Time, branch BranchFilter) (ExpensesReport, error) {
	return svc.store.expensesReport(ctx, tenantID, from, to, branch)
}
