package expenses

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type fixture struct {
	svc  *Service
	pool *pgxpool.Pool
	// tenant A
	tenantA  string
	branchA1 string
	branchA2 string
	userA    string
	// tenant B (aislamiento)
	tenantB  string
	branchB1 string
	userB    string
}

func setup(t *testing.T) *fixture {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL no definido; se omiten tests de integración")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Skipf("pool de test: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Skipf("DB de test no disponible: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`TRUNCATE expenses, expense_concepts, expense_categories, sale_items, sales,
		 user_branches, products, categories, customers, users, branches, tenants
		 RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("truncate: %v", err)
	}

	f := &fixture{svc: NewService(pool), pool: pool}
	q := func(dst *string, sql string, args ...any) {
		if err := pool.QueryRow(ctx, sql, args...).Scan(dst); err != nil {
			t.Fatalf("seed (%s): %v", sql, err)
		}
	}
	q(&f.tenantA, "INSERT INTO tenants (name) VALUES ('A') RETURNING id::text")
	q(&f.tenantB, "INSERT INTO tenants (name) VALUES ('B') RETURNING id::text")
	q(&f.branchA1, "INSERT INTO branches (tenant_id, name) VALUES ($1,'A-Centro') RETURNING id::text", f.tenantA)
	q(&f.branchA2, "INSERT INTO branches (tenant_id, name) VALUES ($1,'A-Norte') RETURNING id::text", f.tenantA)
	q(&f.branchB1, "INSERT INTO branches (tenant_id, name) VALUES ($1,'B-Centro') RETURNING id::text", f.tenantB)
	q(&f.userA, "INSERT INTO users (tenant_id, email, password_hash, name, role) VALUES ($1,'a@t.test','x','Ana','cashier') RETURNING id::text", f.tenantA)
	q(&f.userB, "INSERT INTO users (tenant_id, email, password_hash, name, role) VALUES ($1,'b@t.test','x','Beto','cashier') RETURNING id::text", f.tenantB)
	return f
}

// TestCatalogCRUD cubre categorías/conceptos: creación, name_taken, categoría ajena.
func TestCatalogCRUD(t *testing.T) {
	f := setup(t)
	defer f.pool.Close()
	ctx := context.Background()

	cat, err := f.svc.CreateCategory(ctx, f.tenantA, "  Servicios  ", 1)
	if err != nil {
		t.Fatalf("crear categoría: %v", err)
	}
	if cat.Name != "Servicios" {
		t.Fatalf("nombre debía trimear a 'Servicios', obtuvo %q", cat.Name)
	}

	// Nombre vacío -> validación.
	if _, err := f.svc.CreateCategory(ctx, f.tenantA, "   ", 0); !errors.Is(err, ErrValidation) {
		t.Fatalf("categoría vacía: esperaba ErrValidation, obtuvo %v", err)
	}
	// Duplicado -> name_taken.
	if _, err := f.svc.CreateCategory(ctx, f.tenantA, "Servicios", 0); !errors.Is(err, ErrNameTaken) {
		t.Fatalf("categoría duplicada: esperaba ErrNameTaken, obtuvo %v", err)
	}

	// Concepto con categoría propia -> ok, con categoryName.
	con, err := f.svc.CreateConcept(ctx, f.tenantA, &cat.ID, "Luz")
	if err != nil {
		t.Fatalf("crear concepto: %v", err)
	}
	if con.CategoryName == nil || *con.CategoryName != "Servicios" {
		t.Fatalf("concepto debía traer categoryName 'Servicios', obtuvo %+v", con.CategoryName)
	}

	// Concepto sin categoría -> categoryName nil.
	noCat, err := f.svc.CreateConcept(ctx, f.tenantA, nil, "Varios")
	if err != nil {
		t.Fatalf("crear concepto sin categoría: %v", err)
	}
	if noCat.CategoryID != nil || noCat.CategoryName != nil {
		t.Fatalf("concepto sin categoría debía tener category nil, obtuvo %+v/%+v", noCat.CategoryID, noCat.CategoryName)
	}

	// Concepto duplicado -> name_taken.
	if _, err := f.svc.CreateConcept(ctx, f.tenantA, &cat.ID, "Luz"); !errors.Is(err, ErrNameTaken) {
		t.Fatalf("concepto duplicado: esperaba ErrNameTaken, obtuvo %v", err)
	}

	// Categoría de OTRO tenant -> invalid_category (no se puede colgar de ajena).
	catB, err := f.svc.CreateCategory(ctx, f.tenantB, "Ajena", 0)
	if err != nil {
		t.Fatalf("crear categoría B: %v", err)
	}
	if _, err := f.svc.CreateConcept(ctx, f.tenantA, &catB.ID, "Colada"); !errors.Is(err, ErrInvalidCategory) {
		t.Fatalf("concepto con categoría ajena: esperaba ErrInvalidCategory, obtuvo %v", err)
	}

	// Update de concepto: cambia nombre y lo desactiva.
	inactive := "inactive"
	newName := "Energía eléctrica"
	upd, err := f.svc.UpdateConcept(ctx, f.tenantA, con.ID, ConceptUpdate{Name: &newName, Status: &inactive})
	if err != nil {
		t.Fatalf("update concepto: %v", err)
	}
	if upd.Name != "Energía eléctrica" || upd.Status != "inactive" {
		t.Fatalf("update concepto inesperado: %+v", upd)
	}
	// Reasignar categoría ajena en update -> invalid_category.
	if _, err := f.svc.UpdateConcept(ctx, f.tenantA, con.ID, ConceptUpdate{CategoryID: &catB.ID}); !errors.Is(err, ErrInvalidCategory) {
		t.Fatalf("update concepto categoría ajena: esperaba ErrInvalidCategory, obtuvo %v", err)
	}

	// Listado por tenant: A tiene 2 conceptos (Luz->Energía, Varios); B ninguno.
	list, err := f.svc.ListConcepts(ctx, f.tenantA)
	if err != nil {
		t.Fatalf("listar conceptos: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("A esperaba 2 conceptos, obtuvo %d", len(list))
	}
	listB, _ := f.svc.ListConcepts(ctx, f.tenantB)
	if len(listB) != 0 {
		t.Fatalf("B esperaba 0 conceptos, obtuvo %d", len(listB))
	}
}

// TestCreateExpense cubre snapshot, concepto inactivo/ajeno y monto <= 0.
func TestCreateExpense(t *testing.T) {
	f := setup(t)
	defer f.pool.Close()
	ctx := context.Background()

	cat, _ := f.svc.CreateCategory(ctx, f.tenantA, "Servicios", 0)
	con, _ := f.svc.CreateConcept(ctx, f.tenantA, &cat.ID, "Luz")

	// Gasto válido: snapshot de nombre y campos derivados.
	e, err := f.svc.CreateExpense(ctx, f.tenantA, f.branchA1, con.ID, 15000, f.userA)
	if err != nil {
		t.Fatalf("crear gasto: %v", err)
	}
	if e.ConceptName != "Luz" || e.AmountCents != 15000 {
		t.Fatalf("gasto inesperado: %+v", e)
	}
	if e.BranchName == nil || *e.BranchName != "A-Centro" {
		t.Fatalf("branchName esperaba A-Centro, obtuvo %+v", e.BranchName)
	}
	if e.CategoryName == nil || *e.CategoryName != "Servicios" {
		t.Fatalf("categoryName esperaba Servicios, obtuvo %+v", e.CategoryName)
	}
	if e.CreatedByName == nil || *e.CreatedByName != "Ana" {
		t.Fatalf("createdByName esperaba Ana, obtuvo %+v", e.CreatedByName)
	}

	// Snapshot: renombrar el concepto NO cambia el gasto ya registrado.
	newName := "Energía"
	f.svc.UpdateConcept(ctx, f.tenantA, con.ID, ConceptUpdate{Name: &newName})
	list, _ := f.svc.ListExpenses(ctx, f.tenantA, &f.branchA1, nil, nil)
	if len(list) != 1 || list[0].ConceptName != "Luz" {
		t.Fatalf("snapshot: el gasto debía conservar 'Luz', obtuvo %+v", list)
	}

	// Monto <= 0 -> validación.
	if _, err := f.svc.CreateExpense(ctx, f.tenantA, f.branchA1, con.ID, 0, f.userA); !errors.Is(err, ErrValidation) {
		t.Fatalf("monto 0: esperaba ErrValidation, obtuvo %v", err)
	}
	if _, err := f.svc.CreateExpense(ctx, f.tenantA, f.branchA1, con.ID, -100, f.userA); !errors.Is(err, ErrValidation) {
		t.Fatalf("monto negativo: esperaba ErrValidation, obtuvo %v", err)
	}

	// Concepto inactivo -> invalid_concept.
	inactive := "inactive"
	f.svc.UpdateConcept(ctx, f.tenantA, con.ID, ConceptUpdate{Status: &inactive})
	if _, err := f.svc.CreateExpense(ctx, f.tenantA, f.branchA1, con.ID, 100, f.userA); !errors.Is(err, ErrInvalidConcept) {
		t.Fatalf("concepto inactivo: esperaba ErrInvalidConcept, obtuvo %v", err)
	}

	// Concepto de OTRO tenant -> invalid_concept.
	catB, _ := f.svc.CreateCategory(ctx, f.tenantB, "SB", 0)
	conB, _ := f.svc.CreateConcept(ctx, f.tenantB, &catB.ID, "AguaB")
	if _, err := f.svc.CreateExpense(ctx, f.tenantA, f.branchA1, conB.ID, 100, f.userA); !errors.Is(err, ErrInvalidConcept) {
		t.Fatalf("concepto ajeno: esperaba ErrInvalidConcept, obtuvo %v", err)
	}
}

// TestListExpenses cubre filtro por sucursal, rango [from,to) y aislamiento tenant.
func TestListExpenses(t *testing.T) {
	f := setup(t)
	defer f.pool.Close()
	ctx := context.Background()

	cat, _ := f.svc.CreateCategory(ctx, f.tenantA, "Servicios", 0)
	con, _ := f.svc.CreateConcept(ctx, f.tenantA, &cat.ID, "Luz")

	// A1: dos gastos; A2: uno; B1: uno (aislamiento).
	f.svc.CreateExpense(ctx, f.tenantA, f.branchA1, con.ID, 100, f.userA)
	f.svc.CreateExpense(ctx, f.tenantA, f.branchA1, con.ID, 200, f.userA)
	f.svc.CreateExpense(ctx, f.tenantA, f.branchA2, con.ID, 300, f.userA)
	catB, _ := f.svc.CreateCategory(ctx, f.tenantB, "SB", 0)
	conB, _ := f.svc.CreateConcept(ctx, f.tenantB, &catB.ID, "AguaB")
	f.svc.CreateExpense(ctx, f.tenantB, f.branchB1, conB.ID, 999, f.userB)

	// Sucursal A1 -> 2 gastos.
	l1, err := f.svc.ListExpenses(ctx, f.tenantA, &f.branchA1, nil, nil)
	if err != nil {
		t.Fatalf("listar A1: %v", err)
	}
	if len(l1) != 2 {
		t.Fatalf("A1 esperaba 2 gastos, obtuvo %d", len(l1))
	}

	// Todo el tenant A (branchID nil) -> 3 gastos, sin ver a B.
	lall, _ := f.svc.ListExpenses(ctx, f.tenantA, nil, nil, nil)
	if len(lall) != 3 {
		t.Fatalf("tenant A esperaba 3 gastos, obtuvo %d", len(lall))
	}

	// Aislamiento: tenant B solo ve su gasto.
	lb, _ := f.svc.ListExpenses(ctx, f.tenantB, nil, nil, nil)
	if len(lb) != 1 || lb[0].AmountCents != 999 {
		t.Fatalf("tenant B aislamiento: %+v", lb)
	}

	// Rango [from,to): una ventana futura excluye todo.
	future := time.Now().Add(time.Hour)
	lr, _ := f.svc.ListExpenses(ctx, f.tenantA, &f.branchA1, &future, nil)
	if len(lr) != 0 {
		t.Fatalf("rango futuro debía excluir todo, obtuvo %d", len(lr))
	}
	past := time.Now().Add(-time.Hour)
	upper := time.Now().Add(time.Hour)
	lr2, _ := f.svc.ListExpenses(ctx, f.tenantA, &f.branchA1, &past, &upper)
	if len(lr2) != 2 {
		t.Fatalf("rango [-1h,+1h) esperaba 2, obtuvo %d", len(lr2))
	}
}

// TestDeleteTenantIsolation verifica que borrar un gasto de otro tenant es 404.
func TestDeleteTenantIsolation(t *testing.T) {
	f := setup(t)
	defer f.pool.Close()
	ctx := context.Background()

	catB, _ := f.svc.CreateCategory(ctx, f.tenantB, "SB", 0)
	conB, _ := f.svc.CreateConcept(ctx, f.tenantB, &catB.ID, "AguaB")
	eB, _ := f.svc.CreateExpense(ctx, f.tenantB, f.branchB1, conB.ID, 500, f.userB)

	// Tenant A intenta leer el gasto de B para borrarlo -> ErrNotFound.
	if _, _, err := f.svc.ExpenseForDelete(ctx, f.tenantA, eB.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ExpenseForDelete cross-tenant: esperaba ErrNotFound, obtuvo %v", err)
	}
	if err := f.svc.DeleteExpense(ctx, f.tenantA, eB.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("DeleteExpense cross-tenant: esperaba ErrNotFound, obtuvo %v", err)
	}
	// El dueño sí puede.
	if err := f.svc.DeleteExpense(ctx, f.tenantB, eB.ID); err != nil {
		t.Fatalf("DeleteExpense dueño: %v", err)
	}
	// Uuid mal formado -> ErrNotFound (no 500).
	if _, _, err := f.svc.ExpenseForDelete(ctx, f.tenantA, "no-es-uuid"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ExpenseForDelete uuid inválido: esperaba ErrNotFound, obtuvo %v", err)
	}
}
