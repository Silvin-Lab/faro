package dayclose

import (
	"context"
	"strings"
	"time"

	// tzdata embebida (igual criterio que internal/auth): la imagen de producción es
	// distroless y no trae /usr/share/zoneinfo; sin esto LoadLocation fallaría en runtime.
	_ "time/tzdata"

	"github.com/jackc/pgx/v5/pgxpool"

	"faro/internal/reports"
)

const mexicoTZ = "America/Mexico_City"

// Service orquesta el cierre de día. Llama directamente al módulo reports (sin HTTP) para
// los totales en vivo. Tenant-scoped; el gating por rol lo aplican los handlers (inline).
type Service struct {
	store   *store
	reports *reports.Service
	now     func() time.Time // inyectable en tests; nil => time.Now
}

func NewService(pool *pgxpool.Pool, reportsSvc *reports.Service) *Service {
	return &Service{store: newStore(pool), reports: reportsSvc}
}

func (svc *Service) nowTime() time.Time {
	if svc.now != nil {
		return svc.now()
	}
	return time.Now()
}

// mexicoLocation carga la zona de México (tzdata embebida); fallback defensivo UTC-6.
func mexicoLocation() *time.Location {
	loc, err := time.LoadLocation(mexicoTZ)
	if err != nil {
		return time.FixedZone("CST", -6*60*60)
	}
	return loc
}

// today devuelve la fecha de hoy en zona México (YYYY-MM-DD).
func (svc *Service) today() string {
	return svc.nowTime().In(mexicoLocation()).Format("2006-01-02")
}

// normalizeDate valida y normaliza una fecha YYYY-MM-DD; vacía => hoy (México). Fecha
// inválida => ErrValidation.
func (svc *Service) normalizeDate(date string) (string, error) {
	date = strings.TrimSpace(date)
	if date == "" {
		return svc.today(), nil
	}
	if _, err := time.Parse("2006-01-02", date); err != nil {
		return "", ErrValidation
	}
	return date, nil
}

// dayRange devuelve el rango [inicio, fin) del día (fecha YYYY-MM-DD) en zona México, como
// instantes para los reportes.
func dayRange(date string) (time.Time, time.Time, error) {
	loc := mexicoLocation()
	d, err := time.ParseInLocation("2006-01-02", date, loc)
	if err != nil {
		return time.Time{}, time.Time{}, ErrValidation
	}
	from := time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, loc)
	return from, from.AddDate(0, 0, 1), nil
}

// liveTotals calcula los totales del día EN VIVO desde reports: ventas totales, gastos
// totales y el efectivo esperado (ventas en efectivo − gastos), para la sucursal y fecha.
func (svc *Service) liveTotals(ctx context.Context, tenantID, branchID, date string) (totalSales, totalExpenses, cashExpected int, err error) {
	from, to, err := dayRange(date)
	if err != nil {
		return 0, 0, 0, err
	}
	branch := reports.BranchFilter{ID: &branchID}
	sales, err := svc.reports.SalesReport(ctx, tenantID, from, to, 0, branch)
	if err != nil {
		return 0, 0, 0, err
	}
	exp, err := svc.reports.ExpensesReport(ctx, tenantID, from, to, branch)
	if err != nil {
		return 0, 0, 0, err
	}
	var cashSales int
	for _, pm := range sales.ByPaymentMethod {
		if pm.Method == "cash" {
			cashSales = pm.TotalCents
			break
		}
	}
	totalSales = sales.TotalCents
	totalExpenses = exp.Summary.TotalCents
	cashExpected = cashSales - totalExpenses
	return totalSales, totalExpenses, cashExpected, nil
}

// decorateLive rellena los totales EN VIVO de un cierre en 'draft' (no persiste). Los
// cierres 'submitted' se devuelven con su snapshot congelado tal cual.
func (svc *Service) decorateLive(ctx context.Context, tenantID string, c Closure) (Closure, error) {
	if c.Status != "draft" {
		return c, nil
	}
	ts, te, ce, err := svc.liveTotals(ctx, tenantID, c.BranchID, c.ClosureDate)
	if err != nil {
		return Closure{}, err
	}
	c.TotalSalesCents = &ts
	c.TotalExpensesCents = &te
	c.CashExpectedCents = &ce
	if c.CashCountedCents != nil {
		d := *c.CashCountedCents - ce
		c.CashDiffCents = &d
	} else {
		c.CashDiffCents = nil
	}
	return c, nil
}

// Open es get-or-create idempotente del cierre de (sucursal activa, fecha). Devuelve el
// cierre con totales en vivo si es draft, o el snapshot si es submitted.
func (svc *Service) Open(ctx context.Context, tenantID, branchID, date, createdBy string) (Closure, error) {
	date, err := svc.normalizeDate(date)
	if err != nil {
		return Closure{}, err
	}
	c, _, err := svc.store.getOrCreate(ctx, tenantID, branchID, date, createdBy)
	if err != nil {
		return Closure{}, err
	}
	return svc.decorateLive(ctx, tenantID, c)
}

// Get devuelve un cierre por id (con totales en vivo si draft).
func (svc *Service) Get(ctx context.Context, tenantID, id string) (Closure, error) {
	c, err := svc.store.getByID(ctx, tenantID, id)
	if err != nil {
		return Closure{}, err
	}
	return svc.decorateLive(ctx, tenantID, c)
}

// GetRaw devuelve un cierre por id SIN decorar (para el gating: basta la sucursal dueña).
func (svc *Service) GetRaw(ctx context.Context, tenantID, id string) (Closure, error) {
	return svc.store.getByID(ctx, tenantID, id)
}

// UpdateDraft aplica el patch parcial a un cierre en draft y devuelve el cierre con totales
// en vivo. Valida que los enlaces (bakeryCountId/supplyRequisitionId) sean de la misma
// sucursal.
func (svc *Service) UpdateDraft(ctx context.Context, tenantID, id string, p DraftPatch) (Closure, error) {
	c, err := svc.store.getByID(ctx, tenantID, id)
	if err != nil {
		return Closure{}, err
	}
	if c.Status != "draft" {
		return Closure{}, ErrInvalidState
	}
	if p.CashCountedCents != nil && *p.CashCountedCents < 0 {
		return Closure{}, ErrValidation
	}
	if p.BakeryCountID != nil {
		ok, err := svc.store.bakeryCountInBranch(ctx, tenantID, c.BranchID, *p.BakeryCountID)
		if err != nil {
			return Closure{}, err
		}
		if !ok {
			return Closure{}, ErrValidation
		}
	}
	if p.SupplyRequisitionID != nil {
		ok, err := svc.store.requisitionInBranch(ctx, tenantID, c.BranchID, *p.SupplyRequisitionID)
		if err != nil {
			return Closure{}, err
		}
		if !ok {
			return Closure{}, ErrValidation
		}
	}
	updated, err := svc.store.updateDraft(ctx, tenantID, id, p)
	if err != nil {
		return Closure{}, err
	}
	return svc.decorateLive(ctx, tenantID, updated)
}

// Submit recalcula los totales en vivo una última vez, los persiste y pasa el cierre a
// submitted. Ya submitted => ErrInvalidState (el handler lo mapea a 409).
func (svc *Service) Submit(ctx context.Context, tenantID, id, submittedBy string) (Closure, error) {
	c, err := svc.store.getByID(ctx, tenantID, id)
	if err != nil {
		return Closure{}, err
	}
	if c.Status != "draft" {
		return Closure{}, ErrInvalidState
	}
	ts, te, ce, err := svc.liveTotals(ctx, tenantID, c.BranchID, c.ClosureDate)
	if err != nil {
		return Closure{}, err
	}
	return svc.store.submit(ctx, tenantID, id, ts, te, ce, submittedBy)
}

// List lista cierres. branchID fuerza el scope (rol de sucursal); nil = todas (super_admin).
func (svc *Service) List(ctx context.Context, tenantID string, branchID, from, to *string) ([]Closure, error) {
	return svc.store.list(ctx, tenantID, branchID, from, to)
}
