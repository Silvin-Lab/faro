package expenses

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound        = errors.New("not found")
	ErrNameTaken       = errors.New("name taken")
	ErrInvalidCategory = errors.New("invalid category")
	ErrInvalidConcept  = errors.New("invalid concept")
)

type store struct {
	pool *pgxpool.Pool
}

func newStore(pool *pgxpool.Pool) *store {
	return &store{pool: pool}
}

func pgCode(err error, code string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == code
}

// ---- Categorías de gasto ---------------------------------------------------

func (s *store) createCategory(ctx context.Context, tenantID, name string, sortOrder int) (Category, error) {
	var c Category
	err := s.pool.QueryRow(ctx,
		`INSERT INTO expense_categories (tenant_id, name, sort_order)
		 VALUES ($1, $2, $3)
		 RETURNING id::text, tenant_id::text, name, status, sort_order, created_at`,
		tenantID, name, sortOrder).
		Scan(&c.ID, &c.TenantID, &c.Name, &c.Status, &c.SortOrder, &c.CreatedAt)
	if pgCode(err, "23505") {
		return Category{}, ErrNameTaken
	}
	return c, err
}

func (s *store) listCategories(ctx context.Context, tenantID string) ([]Category, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id::text, tenant_id::text, name, status, sort_order, created_at
		   FROM expense_categories WHERE tenant_id = $1 ORDER BY sort_order, name`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Category
	for rows.Next() {
		var c Category
		if err := rows.Scan(&c.ID, &c.TenantID, &c.Name, &c.Status, &c.SortOrder, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *store) updateCategory(ctx context.Context, tenantID, id string, name, status *string, sortOrder *int) (Category, error) {
	var c Category
	err := s.pool.QueryRow(ctx,
		`UPDATE expense_categories
		    SET name       = COALESCE($3, name),
		        status     = COALESCE($4, status),
		        sort_order = COALESCE($5, sort_order)
		  WHERE id = $1 AND tenant_id = $2
		  RETURNING id::text, tenant_id::text, name, status, sort_order, created_at`,
		id, tenantID, name, status, sortOrder).
		Scan(&c.ID, &c.TenantID, &c.Name, &c.Status, &c.SortOrder, &c.CreatedAt)
	switch {
	case errors.Is(err, pgx.ErrNoRows), pgCode(err, "22P02"):
		return Category{}, ErrNotFound
	case pgCode(err, "23505"):
		return Category{}, ErrNameTaken
	case err != nil:
		return Category{}, err
	}
	return c, nil
}

// categoryExists indica si la categoría pertenece al tenant. Un uuid mal formado
// (22P02) se trata como inexistente.
func (s *store) categoryExists(ctx context.Context, tenantID, id string) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM expense_categories WHERE id = $1 AND tenant_id = $2)`,
		id, tenantID).Scan(&ok)
	if pgCode(err, "22P02") {
		return false, nil
	}
	return ok, err
}

// ---- Conceptos de gasto ----------------------------------------------------

func (s *store) createConcept(ctx context.Context, tenantID string, categoryID *string, name string) (Concept, error) {
	var c Concept
	err := s.pool.QueryRow(ctx,
		`WITH ins AS (
		     INSERT INTO expense_concepts (tenant_id, category_id, name)
		     VALUES ($1, $2, $3)
		     RETURNING id, tenant_id, category_id, name, status, created_at
		 )
		 SELECT i.id::text, i.tenant_id::text, i.category_id::text, cat.name,
		        i.name, i.status, i.created_at
		   FROM ins i
		   LEFT JOIN expense_categories cat ON cat.id = i.category_id`,
		tenantID, categoryID, name).
		Scan(&c.ID, &c.TenantID, &c.CategoryID, &c.CategoryName, &c.Name, &c.Status, &c.CreatedAt)
	if pgCode(err, "23505") {
		return Concept{}, ErrNameTaken
	}
	return c, err
}

func (s *store) listConcepts(ctx context.Context, tenantID string) ([]Concept, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT c.id::text, c.tenant_id::text, c.category_id::text, cat.name,
		        c.name, c.status, c.created_at
		   FROM expense_concepts c
		   LEFT JOIN expense_categories cat ON cat.id = c.category_id
		  WHERE c.tenant_id = $1
		  ORDER BY cat.name NULLS LAST, c.name`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Concept
	for rows.Next() {
		var c Concept
		if err := rows.Scan(&c.ID, &c.TenantID, &c.CategoryID, &c.CategoryName, &c.Name, &c.Status, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// updateConcept aplica cambios parciales. setCategory=true reemplaza category_id
// por categoryID (que ya debe estar validado como del tenant en la capa service).
func (s *store) updateConcept(ctx context.Context, tenantID, id string, name, status *string, categoryID *string, setCategory bool) (Concept, error) {
	var c Concept
	err := s.pool.QueryRow(ctx,
		`WITH upd AS (
		     UPDATE expense_concepts
		        SET name        = COALESCE($3, name),
		            status      = COALESCE($4, status),
		            category_id = CASE WHEN $6 THEN $5 ELSE category_id END
		      WHERE id = $1 AND tenant_id = $2
		      RETURNING id, tenant_id, category_id, name, status, created_at
		 )
		 SELECT u.id::text, u.tenant_id::text, u.category_id::text, cat.name,
		        u.name, u.status, u.created_at
		   FROM upd u
		   LEFT JOIN expense_categories cat ON cat.id = u.category_id`,
		id, tenantID, name, status, categoryID, setCategory).
		Scan(&c.ID, &c.TenantID, &c.CategoryID, &c.CategoryName, &c.Name, &c.Status, &c.CreatedAt)
	switch {
	case errors.Is(err, pgx.ErrNoRows), pgCode(err, "22P02"):
		return Concept{}, ErrNotFound
	case pgCode(err, "23505"):
		return Concept{}, ErrNameTaken
	case err != nil:
		return Concept{}, err
	}
	return c, nil
}

// ---- Gastos ----------------------------------------------------------------

// createExpense inserta el gasto snapshoteando concept_name en la MISMA sentencia
// (el concepto debe estar activo y ser del tenant). 0 filas => ErrInvalidConcept.
func (s *store) createExpense(ctx context.Context, tenantID, branchID, conceptID string, amountCents int, createdBy string) (Expense, error) {
	var e Expense
	err := s.pool.QueryRow(ctx,
		`WITH ins AS (
		     INSERT INTO expenses (tenant_id, branch_id, concept_id, concept_name, amount_cents, created_by)
		     SELECT $1, $2, ec.id, ec.name, $4, $5
		       FROM expense_concepts ec
		      WHERE ec.id = $3 AND ec.tenant_id = $1 AND ec.status = 'active'
		     RETURNING id, tenant_id, branch_id, concept_id, concept_name, amount_cents, created_by, created_at
		 )
		 SELECT i.id::text, i.tenant_id::text, i.branch_id::text, b.name,
		        i.concept_id::text, i.concept_name, cat.name,
		        i.amount_cents, i.created_by::text, u.name, i.created_at
		   FROM ins i
		   LEFT JOIN branches b ON b.id = i.branch_id
		   LEFT JOIN expense_concepts ec ON ec.id = i.concept_id
		   LEFT JOIN expense_categories cat ON cat.id = ec.category_id
		   LEFT JOIN users u ON u.id = i.created_by`,
		tenantID, branchID, conceptID, amountCents, createdBy).
		Scan(&e.ID, &e.TenantID, &e.BranchID, &e.BranchName, &e.ConceptID, &e.ConceptName,
			&e.CategoryName, &e.AmountCents, &e.CreatedBy, &e.CreatedByName, &e.CreatedAt)
	switch {
	case errors.Is(err, pgx.ErrNoRows), pgCode(err, "22P02"):
		return Expense{}, ErrInvalidConcept
	case err != nil:
		return Expense{}, err
	}
	return e, nil
}

// listExpenses lista gastos del tenant, acotados por sucursal (si branchID != nil)
// y por rango [from,to) (si se proveen). LIMIT 500 DESC.
func (s *store) listExpenses(ctx context.Context, tenantID string, branchID *string, from, to *time.Time) ([]Expense, error) {
	args := []any{tenantID}
	q := `SELECT e.id::text, e.tenant_id::text, e.branch_id::text, b.name,
	             e.concept_id::text, e.concept_name, cat.name,
	             e.amount_cents, e.created_by::text, u.name, e.created_at
	        FROM expenses e
	        LEFT JOIN branches b ON b.id = e.branch_id
	        LEFT JOIN expense_concepts ec ON ec.id = e.concept_id
	        LEFT JOIN expense_categories cat ON cat.id = ec.category_id
	        LEFT JOIN users u ON u.id = e.created_by
	       WHERE e.tenant_id = $1`
	if branchID != nil {
		args = append(args, *branchID)
		q += ` AND e.branch_id = $2`
	}
	if from != nil {
		args = append(args, *from)
		q += ` AND e.created_at >= $` + strconv.Itoa(len(args))
	}
	if to != nil {
		args = append(args, *to)
		q += ` AND e.created_at < $` + strconv.Itoa(len(args))
	}
	q += ` ORDER BY e.created_at DESC LIMIT 500`

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Expense
	for rows.Next() {
		var e Expense
		if err := rows.Scan(&e.ID, &e.TenantID, &e.BranchID, &e.BranchName, &e.ConceptID,
			&e.ConceptName, &e.CategoryName, &e.AmountCents, &e.CreatedBy, &e.CreatedByName, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// expenseForDelete devuelve la sucursal y la fecha de creación del gasto (para que
// el handler aplique la matriz de autorización por rol). Fuera del tenant o uuid
// mal formado => ErrNotFound (aísla entre negocios y no filtra existencia).
func (s *store) expenseForDelete(ctx context.Context, tenantID, id string) (branchID string, createdAt time.Time, err error) {
	err = s.pool.QueryRow(ctx,
		`SELECT branch_id::text, created_at FROM expenses WHERE id = $1 AND tenant_id = $2`,
		id, tenantID).Scan(&branchID, &createdAt)
	switch {
	case errors.Is(err, pgx.ErrNoRows), pgCode(err, "22P02"):
		return "", time.Time{}, ErrNotFound
	case err != nil:
		return "", time.Time{}, err
	}
	return branchID, createdAt, nil
}

func (s *store) deleteExpense(ctx context.Context, tenantID, id string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM expenses WHERE id = $1 AND tenant_id = $2`, id, tenantID)
	if pgCode(err, "22P02") {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
