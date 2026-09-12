package expenses

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrValidation indica datos inválidos (nombre vacío, monto <= 0, status ilegal).
var ErrValidation = errors.New("validation")

// Service expone la lógica de gastos (catálogo + registro).
type Service struct {
	store *store
}

func NewService(pool *pgxpool.Pool) *Service {
	return &Service{store: newStore(pool)}
}

// ---- Categorías ------------------------------------------------------------

func (svc *Service) CreateCategory(ctx context.Context, tenantID, name string, sortOrder int) (Category, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Category{}, ErrValidation
	}
	return svc.store.createCategory(ctx, tenantID, name, sortOrder)
}

func (svc *Service) ListCategories(ctx context.Context, tenantID string) ([]Category, error) {
	return svc.store.listCategories(ctx, tenantID)
}

// CategoryUpdate agrupa los cambios parciales de una categoría (nil = no cambia).
type CategoryUpdate struct {
	Name      *string
	Status    *string
	SortOrder *int
}

func (svc *Service) UpdateCategory(ctx context.Context, tenantID, id string, in CategoryUpdate) (Category, error) {
	if in.Name != nil {
		n := strings.TrimSpace(*in.Name)
		if n == "" {
			return Category{}, ErrValidation
		}
		in.Name = &n
	}
	if in.Status != nil && *in.Status != "active" && *in.Status != "inactive" {
		return Category{}, ErrValidation
	}
	return svc.store.updateCategory(ctx, tenantID, id, in.Name, in.Status, in.SortOrder)
}

// ---- Conceptos -------------------------------------------------------------

func (svc *Service) CreateConcept(ctx context.Context, tenantID string, categoryID *string, name string) (Concept, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Concept{}, ErrValidation
	}
	categoryID = normalizeID(categoryID)
	if categoryID != nil {
		ok, err := svc.store.categoryExists(ctx, tenantID, *categoryID)
		if err != nil {
			return Concept{}, err
		}
		if !ok {
			return Concept{}, ErrInvalidCategory
		}
	}
	return svc.store.createConcept(ctx, tenantID, categoryID, name)
}

func (svc *Service) ListConcepts(ctx context.Context, tenantID string) ([]Concept, error) {
	return svc.store.listConcepts(ctx, tenantID)
}

// ConceptUpdate agrupa cambios parciales de un concepto. CategoryID nil se ignora
// (no se puede desasignar categoría vía PATCH; se reasigna enviando otra).
type ConceptUpdate struct {
	Name       *string
	Status     *string
	CategoryID *string
}

func (svc *Service) UpdateConcept(ctx context.Context, tenantID, id string, in ConceptUpdate) (Concept, error) {
	if in.Name != nil {
		n := strings.TrimSpace(*in.Name)
		if n == "" {
			return Concept{}, ErrValidation
		}
		in.Name = &n
	}
	if in.Status != nil && *in.Status != "active" && *in.Status != "inactive" {
		return Concept{}, ErrValidation
	}
	catID := normalizeID(in.CategoryID)
	setCategory := catID != nil
	if setCategory {
		ok, err := svc.store.categoryExists(ctx, tenantID, *catID)
		if err != nil {
			return Concept{}, err
		}
		if !ok {
			return Concept{}, ErrInvalidCategory
		}
	}
	return svc.store.updateConcept(ctx, tenantID, id, in.Name, in.Status, catID, setCategory)
}

// ---- Gastos ----------------------------------------------------------------

// CreateExpense registra un gasto. branchID nil => gasto "General" (corporativo, sin
// sucursal); si viene, debe pertenecer al tenant (ErrInvalidBranch si no). El handler
// garantiza que solo el super_admin puede pasar branchID libremente; los usuarios de
// sucursal siempre reciben su sucursal activa.
func (svc *Service) CreateExpense(ctx context.Context, tenantID string, branchID *string, conceptID string, amountCents int, createdBy string) (Expense, error) {
	if strings.TrimSpace(conceptID) == "" {
		return Expense{}, ErrInvalidConcept
	}
	if amountCents <= 0 {
		return Expense{}, ErrValidation
	}
	branchID = normalizeID(branchID)
	if branchID != nil {
		ok, err := svc.store.branchInTenant(ctx, tenantID, *branchID)
		if err != nil {
			return Expense{}, err
		}
		if !ok {
			return Expense{}, ErrInvalidBranch
		}
	}
	return svc.store.createExpense(ctx, tenantID, branchID, conceptID, amountCents, createdBy)
}

func (svc *Service) ListExpenses(ctx context.Context, tenantID string, branchID *string, from, to *time.Time) ([]Expense, error) {
	return svc.store.listExpenses(ctx, tenantID, branchID, from, to)
}

// ExpenseForDelete devuelve la sucursal y fecha del gasto para que el handler
// evalúe la matriz de borrado por rol. branchID nil = gasto "General" (sin sucursal).
// ErrNotFound si no es del tenant.
func (svc *Service) ExpenseForDelete(ctx context.Context, tenantID, id string) (branchID *string, createdAt time.Time, err error) {
	return svc.store.expenseForDelete(ctx, tenantID, id)
}

func (svc *Service) DeleteExpense(ctx context.Context, tenantID, id string) error {
	return svc.store.deleteExpense(ctx, tenantID, id)
}

// normalizeID convierte cadenas vacías en nil (categoría opcional).
func normalizeID(u *string) *string {
	if u == nil || strings.TrimSpace(*u) == "" {
		return nil
	}
	v := strings.TrimSpace(*u)
	return &v
}
