package supplies

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
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
	prodA    string
	// tenant B (aislamiento)
	tenantB  string
	branchB1 string
	prodB    string
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
		`TRUNCATE supply_movements, supply_branch_stock, product_supplies, supply_measures,
		 supplies, supply_categories, sale_items, sales, user_branches, products, categories,
		 customers, users, branches, tenants RESTART IDENTITY CASCADE`); err != nil {
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
	q(&f.userA, "INSERT INTO users (tenant_id, email, password_hash, name, role) VALUES ($1,'a@t.test','x','Ana','branch_admin') RETURNING id::text", f.tenantA)
	q(&f.prodA, "INSERT INTO products (tenant_id, name, price_cents) VALUES ($1,'Latte',5000) RETURNING id::text", f.tenantA)
	q(&f.prodB, "INSERT INTO products (tenant_id, name, price_cents) VALUES ($1,'CafeB',4000) RETURNING id::text", f.tenantB)
	return f
}

// sumMovements devuelve SUM(quantity_base) del ledger para (supply, branch).
func (f *fixture) sumMovements(t *testing.T, supplyID, branchID string) int {
	t.Helper()
	var sum int
	if err := f.pool.QueryRow(context.Background(),
		`SELECT COALESCE(SUM(quantity_base),0) FROM supply_movements WHERE supply_id=$1 AND branch_id=$2`,
		supplyID, branchID).Scan(&sum); err != nil {
		t.Fatalf("sumMovements: %v", err)
	}
	return sum
}

// cacheStock devuelve el stock_base cacheado para (supply, branch); -9999 si no hay fila.
func (f *fixture) cacheStock(t *testing.T, supplyID, branchID string) int {
	t.Helper()
	var stock int
	err := f.pool.QueryRow(context.Background(),
		`SELECT stock_base FROM supply_branch_stock WHERE supply_id=$1 AND branch_id=$2`,
		supplyID, branchID).Scan(&stock)
	if errors.Is(err, pgx.ErrNoRows) {
		return -9999
	}
	if err != nil {
		t.Fatalf("cacheStock: %v", err)
	}
	return stock
}

// TestSupplyCategoryCRUD cubre el catálogo de categorías de insumo: creación,
// name_taken, categoría ajena (not found en update), y actualización parcial.
func TestSupplyCategoryCRUD(t *testing.T) {
	f := setup(t)
	defer f.pool.Close()
	ctx := context.Background()

	c, err := f.svc.CreateCategory(ctx, f.tenantA, "  Lácteos  ", 2)
	if err != nil {
		t.Fatalf("crear categoría: %v", err)
	}
	if c.Name != "Lácteos" || c.Status != "active" || c.SortOrder != 2 {
		t.Fatalf("categoría inesperada: %+v", c)
	}

	// Nombre vacío -> validación.
	if _, err := f.svc.CreateCategory(ctx, f.tenantA, "   ", 0); !errors.Is(err, ErrValidation) {
		t.Fatalf("nombre vacío: esperaba ErrValidation, obtuvo %v", err)
	}
	// Duplicado -> name_taken.
	if _, err := f.svc.CreateCategory(ctx, f.tenantA, "Lácteos", 0); !errors.Is(err, ErrNameTaken) {
		t.Fatalf("nombre duplicado: esperaba ErrNameTaken, obtuvo %v", err)
	}

	// Listado ordenado por sort_order, name.
	if _, err := f.svc.CreateCategory(ctx, f.tenantA, "Abarrotes", 1); err != nil {
		t.Fatalf("crear 2ª categoría: %v", err)
	}
	items, err := f.svc.ListCategories(ctx, f.tenantA)
	if err != nil {
		t.Fatalf("list categorías: %v", err)
	}
	if len(items) != 2 || items[0].Name != "Abarrotes" || items[1].Name != "Lácteos" {
		t.Fatalf("orden inesperado: %+v", items)
	}

	// Update parcial: renombrar + inactivar.
	newName := "Lácteos y quesos"
	inactive := "inactive"
	upd, err := f.svc.UpdateCategory(ctx, f.tenantA, c.ID, CategoryUpdate{Name: &newName, Status: &inactive})
	if err != nil {
		t.Fatalf("update categoría: %v", err)
	}
	if upd.Name != "Lácteos y quesos" || upd.Status != "inactive" || upd.SortOrder != 2 {
		t.Fatalf("update inesperado: %+v", upd)
	}

	// Status ilegal -> validación.
	bad := "archived"
	if _, err := f.svc.UpdateCategory(ctx, f.tenantA, c.ID, CategoryUpdate{Status: &bad}); !errors.Is(err, ErrValidation) {
		t.Fatalf("status ilegal: esperaba ErrValidation, obtuvo %v", err)
	}
	// Categoría de otro tenant -> not found.
	if _, err := f.svc.UpdateCategory(ctx, f.tenantB, c.ID, CategoryUpdate{Name: &newName}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("update cross-tenant: esperaba ErrNotFound, obtuvo %v", err)
	}
	// Tenant B no ve las categorías de A.
	itemsB, _ := f.svc.ListCategories(ctx, f.tenantB)
	if len(itemsB) != 0 {
		t.Fatalf("aislamiento: tenant B esperaba 0 categorías, obtuvo %d", len(itemsB))
	}
}

// TestSupplyWithCategory cubre crear insumo con categoría (categoryName en la
// respuesta), sin categoría (null), categoría ajena (invalid_category), PATCH que
// reasigna categoría, y que list/get devuelven categoryName.
func TestSupplyWithCategory(t *testing.T) {
	f := setup(t)
	defer f.pool.Close()
	ctx := context.Background()

	cat, _ := f.svc.CreateCategory(ctx, f.tenantA, "Lácteos", 0)

	// Crear CON categoría: categoryId/categoryName en la respuesta.
	withCat, err := f.svc.Create(ctx, f.tenantA, "Leche", "ml", "Bote 900 ml", 900, nil, &cat.ID)
	if err != nil {
		t.Fatalf("crear con categoría: %v", err)
	}
	if withCat.CategoryID == nil || *withCat.CategoryID != cat.ID {
		t.Fatalf("crear con categoría: categoryId esperaba %s, obtuvo %v", cat.ID, withCat.CategoryID)
	}
	if withCat.CategoryName == nil || *withCat.CategoryName != "Lácteos" {
		t.Fatalf("crear con categoría: categoryName esperaba Lácteos, obtuvo %v", withCat.CategoryName)
	}

	// Crear SIN categoría: null (cadena vacía se normaliza a nil).
	noCat, err := f.svc.Create(ctx, f.tenantA, "Azúcar", "g", "Bolsa", 1000, nil, nil)
	if err != nil {
		t.Fatalf("crear sin categoría: %v", err)
	}
	if noCat.CategoryID != nil || noCat.CategoryName != nil {
		t.Fatalf("crear sin categoría: esperaba null, obtuvo id=%v name=%v", noCat.CategoryID, noCat.CategoryName)
	}
	empty := "   "
	blankCat, err := f.svc.Create(ctx, f.tenantA, "Sal", "g", "Bolsa", 500, nil, &empty)
	if err != nil {
		t.Fatalf("crear con categoría en blanco: %v", err)
	}
	if blankCat.CategoryID != nil {
		t.Fatalf("categoría en blanco: esperaba null, obtuvo %v", blankCat.CategoryID)
	}

	// Categoría de OTRO tenant -> invalid_category.
	catB, _ := f.svc.CreateCategory(ctx, f.tenantB, "CategoríaB", 0)
	if _, err := f.svc.Create(ctx, f.tenantA, "Café", "g", "Bolsa", 1000, nil, &catB.ID); !errors.Is(err, ErrInvalidCategory) {
		t.Fatalf("categoría ajena en create: esperaba ErrInvalidCategory, obtuvo %v", err)
	}

	// GET round-trip: la categoría persiste.
	gotWith, _ := f.svc.Get(ctx, f.tenantA, withCat.ID)
	if gotWith.CategoryName == nil || *gotWith.CategoryName != "Lácteos" {
		t.Fatalf("GET con categoría: esperaba Lácteos, obtuvo %v", gotWith.CategoryName)
	}
	gotNo, _ := f.svc.Get(ctx, f.tenantA, noCat.ID)
	if gotNo.CategoryName != nil {
		t.Fatalf("GET sin categoría: esperaba null, obtuvo %v", gotNo.CategoryName)
	}

	// PATCH que ASIGNA categoría a un insumo que no la tenía (Azúcar -> Lácteos).
	reassigned, err := f.svc.Update(ctx, f.tenantA, noCat.ID, UpdateInput{CategoryID: &cat.ID})
	if err != nil {
		t.Fatalf("PATCH asigna categoría: %v", err)
	}
	if reassigned.CategoryID == nil || *reassigned.CategoryID != cat.ID || reassigned.CategoryName == nil || *reassigned.CategoryName != "Lácteos" {
		t.Fatalf("PATCH asigna: esperaba Lácteos, obtuvo id=%v name=%v", reassigned.CategoryID, reassigned.CategoryName)
	}

	// PATCH que REASIGNA a otra categoría.
	cat2, _ := f.svc.CreateCategory(ctx, f.tenantA, "Abarrotes", 0)
	reassigned2, err := f.svc.Update(ctx, f.tenantA, noCat.ID, UpdateInput{CategoryID: &cat2.ID})
	if err != nil {
		t.Fatalf("PATCH reasigna categoría: %v", err)
	}
	if reassigned2.CategoryName == nil || *reassigned2.CategoryName != "Abarrotes" {
		t.Fatalf("PATCH reasigna: esperaba Abarrotes, obtuvo %v", reassigned2.CategoryName)
	}

	// PATCH sin categoryId (nil) NO cambia la categoría (puntero nil = no toca).
	other := "Azúcar refinada"
	unchanged, err := f.svc.Update(ctx, f.tenantA, noCat.ID, UpdateInput{Name: &other})
	if err != nil {
		t.Fatalf("PATCH sin categoría: %v", err)
	}
	if unchanged.CategoryName == nil || *unchanged.CategoryName != "Abarrotes" {
		t.Fatalf("PATCH sin categoría: esperaba conservar Abarrotes, obtuvo %v", unchanged.CategoryName)
	}

	// PATCH con categoría ajena -> invalid_category.
	if _, err := f.svc.Update(ctx, f.tenantA, noCat.ID, UpdateInput{CategoryID: &catB.ID}); !errors.Is(err, ErrInvalidCategory) {
		t.Fatalf("categoría ajena en update: esperaba ErrInvalidCategory, obtuvo %v", err)
	}

	// LIST devuelve categoryName por insumo (orden por nombre: Azúcar, Café..., Leche, Sal).
	items, _ := f.svc.List(ctx, f.tenantA)
	byName := map[string]*string{}
	for _, it := range items {
		byName[it.Name] = it.CategoryName
	}
	if byName["Leche"] == nil || *byName["Leche"] != "Lácteos" {
		t.Fatalf("LIST Leche: esperaba Lácteos, obtuvo %v", byName["Leche"])
	}
	if byName["Sal"] != nil {
		t.Fatalf("LIST Sal: esperaba null, obtuvo %v", byName["Sal"])
	}
}

// TestSupplyCategoryOnDeleteSetNull verifica el FK ON DELETE SET NULL: borrar una
// categoría (vía SQL directo, no hay endpoint DELETE) deja los insumos con
// category_id NULL, sin borrarlos.
func TestSupplyCategoryOnDeleteSetNull(t *testing.T) {
	f := setup(t)
	defer f.pool.Close()
	ctx := context.Background()

	cat, _ := f.svc.CreateCategory(ctx, f.tenantA, "Lácteos", 0)
	sp, err := f.svc.Create(ctx, f.tenantA, "Leche", "ml", "Bote", 900, nil, &cat.ID)
	if err != nil {
		t.Fatalf("crear insumo: %v", err)
	}
	if sp.CategoryID == nil {
		t.Fatalf("precondición: el insumo debía tener categoría")
	}

	// Borrar la categoría por SQL directo.
	if _, err := f.pool.Exec(ctx, `DELETE FROM supply_categories WHERE id = $1`, cat.ID); err != nil {
		t.Fatalf("borrar categoría: %v", err)
	}

	// El insumo sigue existiendo pero SIN categoría (category_id NULL).
	got, err := f.svc.Get(ctx, f.tenantA, sp.ID)
	if err != nil {
		t.Fatalf("get tras borrar categoría: %v", err)
	}
	if got.CategoryID != nil || got.CategoryName != nil {
		t.Fatalf("ON DELETE SET NULL: esperaba categoría null, obtuvo id=%v name=%v", got.CategoryID, got.CategoryName)
	}
}

// TestCatalogCRUD cubre create/update, base_unit inválida e inmutable,
// package_content <= 0 y name_taken.
func TestCatalogCRUD(t *testing.T) {
	f := setup(t)
	defer f.pool.Close()
	ctx := context.Background()

	sp, err := f.svc.Create(ctx, f.tenantA, "  Leche  ", "ml", "Bote 900 ml", 900, nil, nil)
	if err != nil {
		t.Fatalf("crear insumo: %v", err)
	}
	if sp.Name != "Leche" || sp.BaseUnit != "ml" || sp.PackageContent != 900 {
		t.Fatalf("insumo inesperado: %+v", sp)
	}

	// base_unit inválida -> validación.
	if _, err := f.svc.Create(ctx, f.tenantA, "Azucar", "kg", "Bolsa", 1000, nil, nil); !errors.Is(err, ErrValidation) {
		t.Fatalf("base_unit inválida: esperaba ErrValidation, obtuvo %v", err)
	}
	// package_content <= 0 -> validación.
	if _, err := f.svc.Create(ctx, f.tenantA, "Sal", "g", "Bolsa", 0, nil, nil); !errors.Is(err, ErrValidation) {
		t.Fatalf("package_content 0: esperaba ErrValidation, obtuvo %v", err)
	}
	if _, err := f.svc.Create(ctx, f.tenantA, "Sal", "g", "Bolsa", -5, nil, nil); !errors.Is(err, ErrValidation) {
		t.Fatalf("package_content negativo: esperaba ErrValidation, obtuvo %v", err)
	}
	// name_taken.
	if _, err := f.svc.Create(ctx, f.tenantA, "Leche", "ml", "Otro", 500, nil, nil); !errors.Is(err, ErrNameTaken) {
		t.Fatalf("nombre duplicado: esperaba ErrNameTaken, obtuvo %v", err)
	}

	// Update: renombrar + inactivar + cambiar presentación.
	newName := "Leche entera"
	inactive := "inactive"
	newPkg := "Bote 1 L"
	newContent := 1000
	upd, err := f.svc.Update(ctx, f.tenantA, sp.ID, UpdateInput{
		Name: &newName, Status: &inactive, PackageName: &newPkg, PackageContent: &newContent,
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if upd.Name != "Leche entera" || upd.Status != "inactive" || upd.PackageContent != 1000 || upd.BaseUnit != "ml" {
		t.Fatalf("update inesperado: %+v", upd)
	}

	// base_unit INMUTABLE: enviarla en el PATCH -> validation_error.
	bu := "g"
	if _, err := f.svc.Update(ctx, f.tenantA, sp.ID, UpdateInput{BaseUnit: &bu}); !errors.Is(err, ErrValidation) {
		t.Fatalf("base_unit inmutable: esperaba ErrValidation, obtuvo %v", err)
	}
	// Incluso enviando la MISMA unidad -> validation_error (no se acepta el campo).
	same := "ml"
	if _, err := f.svc.Update(ctx, f.tenantA, sp.ID, UpdateInput{BaseUnit: &same}); !errors.Is(err, ErrValidation) {
		t.Fatalf("base_unit misma unidad: esperaba ErrValidation, obtuvo %v", err)
	}

	// Insumo de otro tenant -> not found.
	if _, err := f.svc.Update(ctx, f.tenantB, sp.ID, UpdateInput{Name: &newName}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("update cross-tenant: esperaba ErrNotFound, obtuvo %v", err)
	}
}

// TestPackageCost cubre el costo de la presentación: crear con costo, crear sin
// costo (null round-trip por list/get), PATCH del costo y costo negativo
// (validation_error) tanto en create como en update.
func TestPackageCost(t *testing.T) {
	f := setup(t)
	defer f.pool.Close()
	ctx := context.Background()

	// Crear CON costo: "Bote 900 ml" cuesta $85.00 -> 8500 centavos.
	cost := 8500
	withCost, err := f.svc.Create(ctx, f.tenantA, "Leche", "ml", "Bote 900 ml", 900, &cost, nil)
	if err != nil {
		t.Fatalf("crear con costo: %v", err)
	}
	if withCost.PackageCostCents == nil || *withCost.PackageCostCents != 8500 {
		t.Fatalf("crear con costo: esperaba 8500, obtuvo %v", withCost.PackageCostCents)
	}

	// Crear SIN costo: nil = no capturado (NO 0).
	noCost, err := f.svc.Create(ctx, f.tenantA, "Azúcar", "g", "Bolsa 1kg", 1000, nil, nil)
	if err != nil {
		t.Fatalf("crear sin costo: %v", err)
	}
	if noCost.PackageCostCents != nil {
		t.Fatalf("crear sin costo: esperaba nil, obtuvo %v", noCost.PackageCostCents)
	}

	// Round-trip por GET: el costo persiste (con) y el null persiste (sin).
	gotWith, _ := f.svc.Get(ctx, f.tenantA, withCost.ID)
	if gotWith.PackageCostCents == nil || *gotWith.PackageCostCents != 8500 {
		t.Fatalf("GET con costo: esperaba 8500, obtuvo %v", gotWith.PackageCostCents)
	}
	gotNo, _ := f.svc.Get(ctx, f.tenantA, noCost.ID)
	if gotNo.PackageCostCents != nil {
		t.Fatalf("GET sin costo: esperaba nil, obtuvo %v", gotNo.PackageCostCents)
	}

	// Round-trip por LIST (ordenado por nombre: Azúcar, Leche).
	items, _ := f.svc.List(ctx, f.tenantA)
	byName := map[string]*int{}
	for _, it := range items {
		byName[it.Name] = it.PackageCostCents
	}
	if byName["Leche"] == nil || *byName["Leche"] != 8500 {
		t.Fatalf("LIST Leche: esperaba 8500, obtuvo %v", byName["Leche"])
	}
	if byName["Azúcar"] != nil {
		t.Fatalf("LIST Azúcar: esperaba nil, obtuvo %v", byName["Azúcar"])
	}

	// PATCH del costo: capturar el costo antes ausente (Azúcar).
	newCost := 4200
	upd, err := f.svc.Update(ctx, f.tenantA, noCost.ID, UpdateInput{PackageCostCents: &newCost})
	if err != nil {
		t.Fatalf("PATCH costo: %v", err)
	}
	if upd.PackageCostCents == nil || *upd.PackageCostCents != 4200 {
		t.Fatalf("PATCH costo: esperaba 4200, obtuvo %v", upd.PackageCostCents)
	}

	// PATCH sin el campo (nil) NO cambia el costo (puntero nil = no toca).
	other := "Azúcar refinada"
	unchanged, err := f.svc.Update(ctx, f.tenantA, noCost.ID, UpdateInput{Name: &other})
	if err != nil {
		t.Fatalf("PATCH sin costo: %v", err)
	}
	if unchanged.PackageCostCents == nil || *unchanged.PackageCostCents != 4200 {
		t.Fatalf("PATCH sin costo: esperaba conservar 4200, obtuvo %v", unchanged.PackageCostCents)
	}

	// Costo negativo en CREATE -> validation_error.
	neg := -1
	if _, err := f.svc.Create(ctx, f.tenantA, "Sal", "g", "Bolsa", 500, &neg, nil); !errors.Is(err, ErrValidation) {
		t.Fatalf("crear costo negativo: esperaba ErrValidation, obtuvo %v", err)
	}
	// Costo negativo en UPDATE -> validation_error.
	if _, err := f.svc.Update(ctx, f.tenantA, withCost.ID, UpdateInput{PackageCostCents: &neg}); !errors.Is(err, ErrValidation) {
		t.Fatalf("update costo negativo: esperaba ErrValidation, obtuvo %v", err)
	}

	// Costo 0 es un costo VÁLIDO (gratis), distinto de null (desconocido).
	zero := 0
	free, err := f.svc.Create(ctx, f.tenantA, "Agua", "ml", "Garrafón", 20000, &zero, nil)
	if err != nil {
		t.Fatalf("crear costo 0: %v", err)
	}
	if free.PackageCostCents == nil || *free.PackageCostCents != 0 {
		t.Fatalf("crear costo 0: esperaba 0 (no nil), obtuvo %v", free.PackageCostCents)
	}
}

// TestPurchaseMovement cubre compra por presentaciones (2 x 900 = 1800) que solo
// afecta a la sucursal indicada; la otra sucursal queda en 0 (sin fila de cache).
func TestPurchaseMovement(t *testing.T) {
	f := setup(t)
	defer f.pool.Close()
	ctx := context.Background()

	sp, _ := f.svc.Create(ctx, f.tenantA, "Leche", "ml", "Bote 900 ml", 900, nil, nil)

	packages := 2
	m, stock, err := f.svc.CreateMovement(ctx, f.tenantA, sp.ID, MovementInput{
		Type: "purchase", BranchID: f.branchA1, Packages: &packages,
	}, f.userA)
	if err != nil {
		t.Fatalf("compra: %v", err)
	}
	if m.QuantityBase != 1800 || stock != 1800 {
		t.Fatalf("compra 2x900: esperaba +1800 y stock 1800, obtuvo qty=%d stock=%d", m.QuantityBase, stock)
	}
	if m.Type != "purchase" || m.CreatedBy == nil || *m.CreatedBy != f.userA {
		t.Fatalf("movimiento inesperado: %+v", m)
	}

	// La otra sucursal (A2) NO tiene fila de cache -> stock 0 (ausente).
	if s := f.cacheStock(t, sp.ID, f.branchA2); s != -9999 {
		t.Fatalf("A2 no debía tener existencias, obtuvo %d", s)
	}

	// Compra por quantityBase directo en A1: suma sobre el stock previo.
	qty := 100
	_, stock2, err := f.svc.CreateMovement(ctx, f.tenantA, sp.ID, MovementInput{
		Type: "purchase", BranchID: f.branchA1, QuantityBase: &qty,
	}, f.userA)
	if err != nil {
		t.Fatalf("compra directa: %v", err)
	}
	if stock2 != 1900 {
		t.Fatalf("compra directa: esperaba stock 1900, obtuvo %d", stock2)
	}

	// purchase sin packages ni quantity -> validación.
	if _, _, err := f.svc.CreateMovement(ctx, f.tenantA, sp.ID, MovementInput{
		Type: "purchase", BranchID: f.branchA1,
	}, f.userA); !errors.Is(err, ErrValidation) {
		t.Fatalf("compra sin cantidad: esperaba ErrValidation, obtuvo %v", err)
	}
	// purchase con AMBOS packages y quantity -> validación (exactamente uno).
	if _, _, err := f.svc.CreateMovement(ctx, f.tenantA, sp.ID, MovementInput{
		Type: "purchase", BranchID: f.branchA1, Packages: &packages, QuantityBase: &qty,
	}, f.userA); !errors.Is(err, ErrValidation) {
		t.Fatalf("compra con ambos: esperaba ErrValidation, obtuvo %v", err)
	}

	// Invariante: SUM(ledger) == cache para (sp, A1).
	if got, want := f.cacheStock(t, sp.ID, f.branchA1), f.sumMovements(t, sp.ID, f.branchA1); got != want {
		t.Fatalf("invariante rota: cache=%d suma=%d", got, want)
	}
}

// TestAdjustmentMovement cubre ajuste firmado con motivo, ajuste sin motivo
// (validación) y ajuste que deja el stock negativo (SIEMPRE aceptado).
func TestAdjustmentMovement(t *testing.T) {
	f := setup(t)
	defer f.pool.Close()
	ctx := context.Background()

	sp, _ := f.svc.Create(ctx, f.tenantA, "Galletas", "pieza", "Caja 24", 24, nil, nil)

	// Ajuste positivo con motivo.
	up := 24
	reason := "inventario inicial"
	m, stock, err := f.svc.CreateMovement(ctx, f.tenantA, sp.ID, MovementInput{
		Type: "adjustment", BranchID: f.branchA1, QuantityBase: &up, Reason: &reason,
	}, f.userA)
	if err != nil {
		t.Fatalf("ajuste +: %v", err)
	}
	if stock != 24 || m.Reason == nil || *m.Reason != "inventario inicial" {
		t.Fatalf("ajuste + inesperado: stock=%d reason=%v", stock, m.Reason)
	}

	// Ajuste sin motivo -> validación.
	down := -10
	if _, _, err := f.svc.CreateMovement(ctx, f.tenantA, sp.ID, MovementInput{
		Type: "adjustment", BranchID: f.branchA1, QuantityBase: &down,
	}, f.userA); !errors.Is(err, ErrValidation) {
		t.Fatalf("ajuste sin motivo: esperaba ErrValidation, obtuvo %v", err)
	}
	// Motivo en blanco -> validación.
	blank := "   "
	if _, _, err := f.svc.CreateMovement(ctx, f.tenantA, sp.ID, MovementInput{
		Type: "adjustment", BranchID: f.branchA1, QuantityBase: &down, Reason: &blank,
	}, f.userA); !errors.Is(err, ErrValidation) {
		t.Fatalf("ajuste motivo en blanco: esperaba ErrValidation, obtuvo %v", err)
	}
	// quantityBase 0 -> validación.
	zero := 0
	if _, _, err := f.svc.CreateMovement(ctx, f.tenantA, sp.ID, MovementInput{
		Type: "adjustment", BranchID: f.branchA1, QuantityBase: &zero, Reason: &reason,
	}, f.userA); !errors.Is(err, ErrValidation) {
		t.Fatalf("ajuste qty 0: esperaba ErrValidation, obtuvo %v", err)
	}

	// Ajuste de cortesía que deja el stock NEGATIVO -> SIEMPRE aceptado (nada bloqueante).
	big := -100
	courtesy := "regalé 100 galletas"
	_, negStock, err := f.svc.CreateMovement(ctx, f.tenantA, sp.ID, MovementInput{
		Type: "adjustment", BranchID: f.branchA1, QuantityBase: &big, Reason: &courtesy,
	}, f.userA)
	if err != nil {
		t.Fatalf("ajuste cortesía negativo: no debía fallar, obtuvo %v", err)
	}
	if negStock != -76 { // 24 - 100
		t.Fatalf("ajuste cortesía: esperaba stock -76, obtuvo %d", negStock)
	}

	// Invariante tras varios movimientos.
	if got, want := f.cacheStock(t, sp.ID, f.branchA1), f.sumMovements(t, sp.ID, f.branchA1); got != want {
		t.Fatalf("invariante rota: cache=%d suma=%d", got, want)
	}
}

// TestMovementBranchValidation cubre branchId faltante, de otro tenant, y el tipo
// 'sale' rechazado por esta vía.
func TestMovementBranchValidation(t *testing.T) {
	f := setup(t)
	defer f.pool.Close()
	ctx := context.Background()

	sp, _ := f.svc.Create(ctx, f.tenantA, "Leche", "ml", "Bote", 900, nil, nil)
	packages := 1

	// branchId vacío -> validación.
	if _, _, err := f.svc.CreateMovement(ctx, f.tenantA, sp.ID, MovementInput{
		Type: "purchase", BranchID: "", Packages: &packages,
	}, f.userA); !errors.Is(err, ErrValidation) {
		t.Fatalf("branch vacío: esperaba ErrValidation, obtuvo %v", err)
	}
	// branchId de otro tenant -> invalid_branch.
	if _, _, err := f.svc.CreateMovement(ctx, f.tenantA, sp.ID, MovementInput{
		Type: "purchase", BranchID: f.branchB1, Packages: &packages,
	}, f.userA); !errors.Is(err, ErrInvalidBranch) {
		t.Fatalf("branch ajeno: esperaba ErrInvalidBranch, obtuvo %v", err)
	}
	// tipo 'sale' por esta vía -> validación (lo escribe el descuento automático).
	qty := 5
	if _, _, err := f.svc.CreateMovement(ctx, f.tenantA, sp.ID, MovementInput{
		Type: "sale", BranchID: f.branchA1, QuantityBase: &qty,
	}, f.userA); !errors.Is(err, ErrValidation) {
		t.Fatalf("tipo sale: esperaba ErrValidation, obtuvo %v", err)
	}
	// Insumo inexistente -> not found.
	if _, _, err := f.svc.CreateMovement(ctx, f.tenantA, "00000000-0000-0000-0000-000000000000", MovementInput{
		Type: "purchase", BranchID: f.branchA1, Packages: &packages,
	}, f.userA); !errors.Is(err, ErrNotFound) {
		t.Fatalf("insumo inexistente: esperaba ErrNotFound, obtuvo %v", err)
	}
}

// TestListStock verifica que el listado agrupa existencias por sucursal y que solo
// aparecen las sucursales con movimiento.
func TestListStock(t *testing.T) {
	f := setup(t)
	defer f.pool.Close()
	ctx := context.Background()

	sp, _ := f.svc.Create(ctx, f.tenantA, "Leche", "ml", "Bote 900 ml", 900, nil, nil)
	packages := 2
	f.svc.CreateMovement(ctx, f.tenantA, sp.ID, MovementInput{Type: "purchase", BranchID: f.branchA1, Packages: &packages}, f.userA)

	items, err := f.svc.List(ctx, f.tenantA)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("esperaba 1 insumo, obtuvo %d", len(items))
	}
	if len(items[0].Stock) != 1 {
		t.Fatalf("esperaba existencias en 1 sucursal, obtuvo %d", len(items[0].Stock))
	}
	st := items[0].Stock[0]
	if st.BranchID != f.branchA1 || st.BranchName != "A-Centro" || st.StockBase != 1800 {
		t.Fatalf("existencias inesperadas: %+v", st)
	}

	// Aislamiento: tenant B no ve el insumo de A.
	itemsB, _ := f.svc.List(ctx, f.tenantB)
	if len(itemsB) != 0 {
		t.Fatalf("tenant B esperaba 0 insumos, obtuvo %d", len(itemsB))
	}
}

// TestRecipeReplaceAll cubre replace-all (reemplaza filas previas), insumo de otro
// tenant (invalid_supply) y producto ajeno (not found).
func TestRecipeReplaceAll(t *testing.T) {
	f := setup(t)
	defer f.pool.Close()
	ctx := context.Background()

	leche, _ := f.svc.Create(ctx, f.tenantA, "Leche", "ml", "Bote", 900, nil, nil)
	cafe, _ := f.svc.Create(ctx, f.tenantA, "Café", "g", "Bolsa", 1000, nil, nil)
	azucar, _ := f.svc.Create(ctx, f.tenantA, "Azúcar", "g", "Bolsa", 1000, nil, nil)

	// Receta inicial: leche 200 + café 18.
	out, err := f.svc.ReplaceRecipe(ctx, f.tenantA, f.prodA, []recipeItemIn{
		{SupplyID: leche.ID, QuantityBase: 200},
		{SupplyID: cafe.ID, QuantityBase: 18},
	})
	if err != nil {
		t.Fatalf("receta inicial: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("receta inicial esperaba 2 items, obtuvo %d", len(out))
	}

	// Replace-all: ahora solo azúcar 10 (reemplaza las filas previas).
	out2, err := f.svc.ReplaceRecipe(ctx, f.tenantA, f.prodA, []recipeItemIn{
		{SupplyID: azucar.ID, QuantityBase: 10},
	})
	if err != nil {
		t.Fatalf("replace-all: %v", err)
	}
	if len(out2) != 1 || out2[0].SupplyID != azucar.ID || out2[0].QuantityBase != 10 {
		t.Fatalf("replace-all inesperado: %+v", out2)
	}
	// GET confirma que quedó solo azúcar.
	got, _ := f.svc.GetRecipe(ctx, f.tenantA, f.prodA)
	if len(got) != 1 || got[0].SupplyID != azucar.ID {
		t.Fatalf("GetRecipe tras replace: %+v", got)
	}

	// Insumo de OTRO tenant -> invalid_supply (y la receta previa NO se destruye: la tx aborta).
	supB, _ := f.svc.Create(ctx, f.tenantB, "InsumoB", "g", "Bolsa", 500, nil, nil)
	if _, err := f.svc.ReplaceRecipe(ctx, f.tenantA, f.prodA, []recipeItemIn{
		{SupplyID: supB.ID, QuantityBase: 5},
	}); !errors.Is(err, ErrInvalidSupply) {
		t.Fatalf("insumo ajeno: esperaba ErrInvalidSupply, obtuvo %v", err)
	}
	// La receta previa (azúcar) sigue intacta tras el rollback.
	still, _ := f.svc.GetRecipe(ctx, f.tenantA, f.prodA)
	if len(still) != 1 || still[0].SupplyID != azucar.ID {
		t.Fatalf("rollback debía preservar la receta previa, obtuvo %+v", still)
	}

	// quantity_base <= 0 -> validación.
	if _, err := f.svc.ReplaceRecipe(ctx, f.tenantA, f.prodA, []recipeItemIn{
		{SupplyID: leche.ID, QuantityBase: 0},
	}); !errors.Is(err, ErrValidation) {
		t.Fatalf("quantity 0: esperaba ErrValidation, obtuvo %v", err)
	}

	// Producto de OTRO tenant -> not found.
	if _, err := f.svc.ReplaceRecipe(ctx, f.tenantA, f.prodB, []recipeItemIn{
		{SupplyID: leche.ID, QuantityBase: 5},
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("producto ajeno: esperaba ErrNotFound, obtuvo %v", err)
	}

	// Vaciar la receta (lista vacía) -> 0 filas.
	empty, err := f.svc.ReplaceRecipe(ctx, f.tenantA, f.prodA, []recipeItemIn{})
	if err != nil {
		t.Fatalf("vaciar receta: %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("vaciar receta: esperaba 0 items, obtuvo %d", len(empty))
	}
}

// psRow lee (quantity_base, measure_id, measure_count) de la fila de receta de un
// producto+insumo. Devuelve qb=-1 si no hay fila.
func (f *fixture) psRow(t *testing.T, productID, supplyID string) (int, *string, *float64) {
	t.Helper()
	var qb int
	var mID *string
	var mCount *float64
	err := f.pool.QueryRow(context.Background(),
		`SELECT quantity_base, measure_id::text, measure_count
		   FROM product_supplies WHERE product_id=$1 AND supply_id=$2`,
		productID, supplyID).Scan(&qb, &mID, &mCount)
	if errors.Is(err, pgx.ErrNoRows) {
		return -1, nil, nil
	}
	if err != nil {
		t.Fatalf("psRow: %v", err)
	}
	return qb, mID, mCount
}

// simulateSaleDeduct replica el SQL crítico de deductSupplies (internal/sales/
// store.go): lee product_supplies.quantity_base, escribe un movimiento 'sale'
// negativo y decrementa el cache. Se usa para probar la REGRESIÓN sin importar el
// paquete sales: el descuento sigue leyendo quantity_base, intacto ante recetas por
// medida. Devuelve el nuevo stock del insumo en la sucursal.
func (f *fixture) simulateSaleDeduct(t *testing.T, tenantID, productID, branchID string, qty int) {
	t.Helper()
	var saleID string
	if err := f.pool.QueryRow(context.Background(),
		`INSERT INTO sales (tenant_id, branch_id, total_cents, payment_method, amount_paid_cents, change_cents)
		 VALUES ($1,$2,0,'cash',0,0) RETURNING id::text`, tenantID, branchID).Scan(&saleID); err != nil {
		t.Fatalf("seed sale: %v", err)
	}
	if _, err := f.pool.Exec(context.Background(),
		`WITH lineas AS (
		     SELECT product_id, qty FROM unnest($4::uuid[], $5::int[]) AS u(product_id, qty)
		 ),
		 consumo AS (
		     SELECT ps.supply_id, SUM(ps.quantity_base * l.qty)::int AS total
		       FROM lineas l JOIN product_supplies ps
		         ON ps.product_id = l.product_id AND ps.tenant_id = $1
		   GROUP BY ps.supply_id
		 ),
		 mov AS (
		     INSERT INTO supply_movements (tenant_id, supply_id, branch_id, type, quantity_base, sale_id)
		     SELECT $1, supply_id, $3, 'sale', -total, $2 FROM consumo
		     RETURNING supply_id, quantity_base
		 )
		 INSERT INTO supply_branch_stock (tenant_id, supply_id, branch_id, stock_base)
		 SELECT $1, supply_id, $3, quantity_base FROM mov ORDER BY supply_id
		 ON CONFLICT (supply_id, branch_id) DO UPDATE
		    SET stock_base = supply_branch_stock.stock_base + EXCLUDED.stock_base, updated_at = now()`,
		tenantID, saleID, branchID, []string{productID}, []int{qty}); err != nil {
		t.Fatalf("simulateSaleDeduct: %v", err)
	}
}

// TestSupplyMeasureCRUD cubre el CRUD de medidas: creación, baseQuantity<=0
// (validación), name_taken por insumo, medida en insumo ajeno (not found),
// actualización parcial, borrado y aislamiento por tenant.
func TestSupplyMeasureCRUD(t *testing.T) {
	f := setup(t)
	defer f.pool.Close()
	ctx := context.Background()

	choco, _ := f.svc.Create(ctx, f.tenantA, "Chocolate", "g", "Bolsa 1kg", 1000, nil, nil)

	// Crear medida "scoop = 25 g".
	scoop, err := f.svc.CreateMeasure(ctx, f.tenantA, choco.ID, "  scoop  ", 25)
	if err != nil {
		t.Fatalf("crear medida: %v", err)
	}
	if scoop.Name != "scoop" || scoop.BaseQuantity != 25 || scoop.SupplyID != choco.ID {
		t.Fatalf("medida inesperada: %+v", scoop)
	}

	// baseQuantity <= 0 -> validación.
	if _, err := f.svc.CreateMeasure(ctx, f.tenantA, choco.ID, "mal", 0); !errors.Is(err, ErrValidation) {
		t.Fatalf("baseQuantity 0: esperaba ErrValidation, obtuvo %v", err)
	}
	// Nombre vacío -> validación.
	if _, err := f.svc.CreateMeasure(ctx, f.tenantA, choco.ID, "   ", 10); !errors.Is(err, ErrValidation) {
		t.Fatalf("nombre vacío: esperaba ErrValidation, obtuvo %v", err)
	}
	// Nombre duplicado por insumo -> name_taken (UNIQUE supply_id,name).
	if _, err := f.svc.CreateMeasure(ctx, f.tenantA, choco.ID, "scoop", 30); !errors.Is(err, ErrNameTaken) {
		t.Fatalf("nombre duplicado: esperaba ErrNameTaken, obtuvo %v", err)
	}
	// Medida en insumo de OTRO tenant -> not found.
	supB, _ := f.svc.Create(ctx, f.tenantB, "ChocoB", "g", "Bolsa", 1000, nil, nil)
	if _, err := f.svc.CreateMeasure(ctx, f.tenantA, supB.ID, "scoop", 25); !errors.Is(err, ErrNotFound) {
		t.Fatalf("insumo ajeno: esperaba ErrNotFound, obtuvo %v", err)
	}

	// Segunda medida en el mismo insumo (cucharada = 12 g).
	if _, err := f.svc.CreateMeasure(ctx, f.tenantA, choco.ID, "cucharada", 12); err != nil {
		t.Fatalf("2ª medida: %v", err)
	}
	// ListMeasures devuelve ambas, ordenadas por nombre.
	list, err := f.svc.ListMeasures(ctx, f.tenantA, choco.ID)
	if err != nil {
		t.Fatalf("list medidas: %v", err)
	}
	if len(list) != 2 || list[0].Name != "cucharada" || list[1].Name != "scoop" {
		t.Fatalf("list medidas inesperado: %+v", list)
	}
	// ListMeasures de insumo ajeno -> not found.
	if _, err := f.svc.ListMeasures(ctx, f.tenantB, choco.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("list medidas ajeno: esperaba ErrNotFound, obtuvo %v", err)
	}

	// Las medidas aparecen embebidas en Get y List del insumo (siempre array).
	got, _ := f.svc.Get(ctx, f.tenantA, choco.ID)
	if len(got.Measures) != 2 {
		t.Fatalf("Get.Measures esperaba 2, obtuvo %d", len(got.Measures))
	}
	items, _ := f.svc.List(ctx, f.tenantA)
	for _, it := range items {
		if it.ID == choco.ID && len(it.Measures) != 2 {
			t.Fatalf("List.Measures esperaba 2, obtuvo %d", len(it.Measures))
		}
		if it.Measures == nil {
			t.Fatalf("List.Measures debía ser array, no nil (insumo %s)", it.Name)
		}
	}

	// Update parcial: renombrar sin tocar baseQuantity.
	newName := "scoop grande"
	upd, err := f.svc.UpdateMeasure(ctx, f.tenantA, scoop.ID, MeasureUpdate{Name: &newName})
	if err != nil {
		t.Fatalf("update medida: %v", err)
	}
	if upd.Name != "scoop grande" || upd.BaseQuantity != 25 {
		t.Fatalf("update medida inesperado: %+v", upd)
	}
	// baseQuantity <= 0 en update -> validación.
	zero := 0
	if _, err := f.svc.UpdateMeasure(ctx, f.tenantA, scoop.ID, MeasureUpdate{BaseQuantity: &zero}); !errors.Is(err, ErrValidation) {
		t.Fatalf("update baseQuantity 0: esperaba ErrValidation, obtuvo %v", err)
	}
	// Medida de otro tenant -> not found.
	if _, err := f.svc.UpdateMeasure(ctx, f.tenantB, scoop.ID, MeasureUpdate{Name: &newName}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("update medida ajena: esperaba ErrNotFound, obtuvo %v", err)
	}

	// Delete: medida ajena -> not found; propia -> ok; doble delete -> not found.
	if err := f.svc.DeleteMeasure(ctx, f.tenantB, scoop.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete medida ajena: esperaba ErrNotFound, obtuvo %v", err)
	}
	if err := f.svc.DeleteMeasure(ctx, f.tenantA, scoop.ID); err != nil {
		t.Fatalf("delete medida: %v", err)
	}
	if err := f.svc.DeleteMeasure(ctx, f.tenantA, scoop.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete doble: esperaba ErrNotFound, obtuvo %v", err)
	}
}

// TestRecipeByMeasure cubre capturar una línea de receta por medida: quantity_base =
// round(count * base); persistencia de measure_id/measure_count; getRecipe devuelve
// measureName/measureCount; y la medida debe pertenecer al insumo (invalid_measure).
func TestRecipeByMeasure(t *testing.T) {
	f := setup(t)
	defer f.pool.Close()
	ctx := context.Background()

	choco, _ := f.svc.Create(ctx, f.tenantA, "Chocolate", "g", "Bolsa 1kg", 1000, nil, nil)
	scoop, _ := f.svc.CreateMeasure(ctx, f.tenantA, choco.ID, "scoop", 25)
	count := 2.0 // 2 scoops = 50 g

	out, err := f.svc.ReplaceRecipe(ctx, f.tenantA, f.prodA, []recipeItemIn{
		{SupplyID: choco.ID, MeasureID: &scoop.ID, MeasureCount: &count},
	})
	if err != nil {
		t.Fatalf("receta por medida: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("esperaba 1 item, obtuvo %d", len(out))
	}
	it := out[0]
	if it.QuantityBase != 50 {
		t.Fatalf("quantity_base esperaba 50 (2*25), obtuvo %d", it.QuantityBase)
	}
	if it.MeasureID == nil || *it.MeasureID != scoop.ID {
		t.Fatalf("measureId esperaba %s, obtuvo %v", scoop.ID, it.MeasureID)
	}
	if it.MeasureName == nil || *it.MeasureName != "scoop" {
		t.Fatalf("measureName esperaba scoop, obtuvo %v", it.MeasureName)
	}
	if it.MeasureCount == nil || *it.MeasureCount != 2.0 {
		t.Fatalf("measureCount esperaba 2, obtuvo %v", it.MeasureCount)
	}

	// Persistencia: la fila guarda measure_id + measure_count + quantity_base.
	qb, mID, mCount := f.psRow(t, f.prodA, choco.ID)
	if qb != 50 || mID == nil || *mID != scoop.ID || mCount == nil || *mCount != 2.0 {
		t.Fatalf("persistencia: qb=%d mID=%v mCount=%v", qb, mID, mCount)
	}

	// getRecipe round-trip.
	got, _ := f.svc.GetRecipe(ctx, f.tenantA, f.prodA)
	if len(got) != 1 || got[0].QuantityBase != 50 || got[0].MeasureName == nil || *got[0].MeasureName != "scoop" {
		t.Fatalf("getRecipe round-trip: %+v", got)
	}

	// Medida que NO pertenece a ese insumo -> invalid_measure. (Medida de otro insumo.)
	otro, _ := f.svc.Create(ctx, f.tenantA, "Vainilla", "g", "Bolsa", 500, nil, nil)
	otroScoop, _ := f.svc.CreateMeasure(ctx, f.tenantA, otro.ID, "scoop", 20)
	if _, err := f.svc.ReplaceRecipe(ctx, f.tenantA, f.prodA, []recipeItemIn{
		{SupplyID: choco.ID, MeasureID: &otroScoop.ID, MeasureCount: &count},
	}); !errors.Is(err, ErrInvalidMeasure) {
		t.Fatalf("medida de otro insumo: esperaba ErrInvalidMeasure, obtuvo %v", err)
	}

	// Mezcla: una línea por medida y otra en unidad base (measureId nil) conviven.
	azucar, _ := f.svc.Create(ctx, f.tenantA, "Azúcar", "g", "Bolsa", 1000, nil, nil)
	mixed, err := f.svc.ReplaceRecipe(ctx, f.tenantA, f.prodA, []recipeItemIn{
		{SupplyID: choco.ID, MeasureID: &scoop.ID, MeasureCount: &count},
		{SupplyID: azucar.ID, QuantityBase: 10},
	})
	if err != nil {
		t.Fatalf("receta mixta: %v", err)
	}
	byName := map[string]RecipeItem{}
	for _, r := range mixed {
		byName[r.SupplyName] = r
	}
	if byName["Azúcar"].MeasureID != nil || byName["Azúcar"].QuantityBase != 10 {
		t.Fatalf("línea unidad base inesperada: %+v", byName["Azúcar"])
	}
	if byName["Chocolate"].MeasureID == nil || byName["Chocolate"].QuantityBase != 50 {
		t.Fatalf("línea por medida inesperada: %+v", byName["Chocolate"])
	}
}

// TestMeasureEditRecomputesRecipe es el TEST CENTRAL: editar el baseQuantity de una
// medida recomputa, en la misma transacción, el quantity_base de las recetas que la
// usan (recálculo en vivo hacia consumo y costo). El descuento en venta lee ese
// quantity_base, así que también se propaga sin tocar deductSupplies.
func TestMeasureEditRecomputesRecipe(t *testing.T) {
	f := setup(t)
	defer f.pool.Close()
	ctx := context.Background()

	choco, _ := f.svc.Create(ctx, f.tenantA, "Chocolate", "g", "Bolsa 1kg", 1000, nil, nil)
	scoop, _ := f.svc.CreateMeasure(ctx, f.tenantA, choco.ID, "scoop", 25)

	// Receta A: 1 scoop -> 25 g. Receta B (otro producto): 2 scoops -> 50 g.
	one, two := 1.0, 2.0
	if _, err := f.svc.ReplaceRecipe(ctx, f.tenantA, f.prodA, []recipeItemIn{
		{SupplyID: choco.ID, MeasureID: &scoop.ID, MeasureCount: &one},
	}); err != nil {
		t.Fatalf("receta A: %v", err)
	}
	var prodA2 string
	if err := f.pool.QueryRow(ctx,
		"INSERT INTO products (tenant_id, name, price_cents) VALUES ($1,'Mocha',6000) RETURNING id::text", f.tenantA).Scan(&prodA2); err != nil {
		t.Fatalf("seed prodA2: %v", err)
	}
	if _, err := f.svc.ReplaceRecipe(ctx, f.tenantA, prodA2, []recipeItemIn{
		{SupplyID: choco.ID, MeasureID: &scoop.ID, MeasureCount: &two},
	}); err != nil {
		t.Fatalf("receta B: %v", err)
	}

	// Precondición: qb A=25, qb B=50.
	if qb, _, _ := f.psRow(t, f.prodA, choco.ID); qb != 25 {
		t.Fatalf("precondición A: esperaba 25, obtuvo %d", qb)
	}
	if qb, _, _ := f.psRow(t, prodA2, choco.ID); qb != 50 {
		t.Fatalf("precondición B: esperaba 50, obtuvo %d", qb)
	}

	// Editar la medida: scoop 25 g -> 30 g. Recálculo en vivo.
	newBase := 30
	upd, err := f.svc.UpdateMeasure(ctx, f.tenantA, scoop.ID, MeasureUpdate{BaseQuantity: &newBase})
	if err != nil {
		t.Fatalf("editar medida: %v", err)
	}
	if upd.BaseQuantity != 30 {
		t.Fatalf("baseQuantity tras update esperaba 30, obtuvo %d", upd.BaseQuantity)
	}

	// AMBAS recetas recomputadas: A=30 (1*30), B=60 (2*30). measure_count intacto.
	if qb, _, mc := f.psRow(t, f.prodA, choco.ID); qb != 30 || mc == nil || *mc != 1.0 {
		t.Fatalf("recálculo A: esperaba qb=30 count=1, obtuvo qb=%d count=%v", qb, mc)
	}
	if qb, _, mc := f.psRow(t, prodA2, choco.ID); qb != 60 || mc == nil || *mc != 2.0 {
		t.Fatalf("recálculo B: esperaba qb=60 count=2, obtuvo qb=%d count=%v", qb, mc)
	}

	// Editar SOLO el nombre no recomputa quantity_base.
	renamed := "scoop chico"
	if _, err := f.svc.UpdateMeasure(ctx, f.tenantA, scoop.ID, MeasureUpdate{Name: &renamed}); err != nil {
		t.Fatalf("rename medida: %v", err)
	}
	if qb, _, _ := f.psRow(t, f.prodA, choco.ID); qb != 30 {
		t.Fatalf("rename no debía tocar qb: esperaba 30, obtuvo %d", qb)
	}
}

// TestDeleteMeasureFreezesRecipe: borrar una medida en uso deja el quantity_base
// "congelado" (no cambia) y measure_id/measure_count en NULL (ON DELETE SET NULL).
func TestDeleteMeasureFreezesRecipe(t *testing.T) {
	f := setup(t)
	defer f.pool.Close()
	ctx := context.Background()

	choco, _ := f.svc.Create(ctx, f.tenantA, "Chocolate", "g", "Bolsa 1kg", 1000, nil, nil)
	scoop, _ := f.svc.CreateMeasure(ctx, f.tenantA, choco.ID, "scoop", 25)
	count := 2.0
	if _, err := f.svc.ReplaceRecipe(ctx, f.tenantA, f.prodA, []recipeItemIn{
		{SupplyID: choco.ID, MeasureID: &scoop.ID, MeasureCount: &count},
	}); err != nil {
		t.Fatalf("receta: %v", err)
	}
	if qb, _, _ := f.psRow(t, f.prodA, choco.ID); qb != 50 {
		t.Fatalf("precondición: esperaba qb=50, obtuvo %d", qb)
	}

	// Borrar la medida.
	if err := f.svc.DeleteMeasure(ctx, f.tenantA, scoop.ID); err != nil {
		t.Fatalf("borrar medida: %v", err)
	}

	// quantity_base congelado en 50; measure_id en NULL (ON DELETE SET NULL solo anula
	// la FK; la columna cruda measure_count conserva su valor histórico, pero la API lo
	// oculta al no haber measure_id — se verifica abajo vía getRecipe).
	qb, mID, _ := f.psRow(t, f.prodA, choco.ID)
	if qb != 50 {
		t.Fatalf("congelado: quantity_base esperaba 50, obtuvo %d", qb)
	}
	if mID != nil {
		t.Fatalf("congelado: measure_id esperaba NULL, obtuvo %v", mID)
	}
	// getRecipe: measureId/measureName/measureCount todos nil, quantity_base sigue 50.
	got, _ := f.svc.GetRecipe(ctx, f.tenantA, f.prodA)
	if len(got) != 1 || got[0].QuantityBase != 50 || got[0].MeasureID != nil || got[0].MeasureName != nil || got[0].MeasureCount != nil {
		t.Fatalf("getRecipe tras borrar medida: %+v", got)
	}
}

// TestRecipeMeasureRoundsToZero: si round(count * base) < 1 (redondea a 0), la línea
// no tiene sentido físico y viola el CHECK(quantity_base>0) -> validation_error.
func TestRecipeMeasureRoundsToZero(t *testing.T) {
	f := setup(t)
	defer f.pool.Close()
	ctx := context.Background()

	choco, _ := f.svc.Create(ctx, f.tenantA, "Chocolate", "g", "Bolsa", 1000, nil, nil)
	tiny, _ := f.svc.CreateMeasure(ctx, f.tenantA, choco.ID, "pizca", 1) // 1 g por pizca
	small := 0.4                                                         // round(0.4*1)=0

	if _, err := f.svc.ReplaceRecipe(ctx, f.tenantA, f.prodA, []recipeItemIn{
		{SupplyID: choco.ID, MeasureID: &tiny.ID, MeasureCount: &small},
	}); !errors.Is(err, ErrValidation) {
		t.Fatalf("round a 0: esperaba ErrValidation, obtuvo %v", err)
	}
	// measureCount <= 0 -> validación.
	zero := 0.0
	if _, err := f.svc.ReplaceRecipe(ctx, f.tenantA, f.prodA, []recipeItemIn{
		{SupplyID: choco.ID, MeasureID: &tiny.ID, MeasureCount: &zero},
	}); !errors.Is(err, ErrValidation) {
		t.Fatalf("measureCount 0: esperaba ErrValidation, obtuvo %v", err)
	}
	// round(0.5*1)=1 (Go math.Round redondea 0.5 hacia arriba en magnitud) -> válido.
	half := 0.5
	out, err := f.svc.ReplaceRecipe(ctx, f.tenantA, f.prodA, []recipeItemIn{
		{SupplyID: choco.ID, MeasureID: &tiny.ID, MeasureCount: &half},
	})
	if err != nil {
		t.Fatalf("round 0.5->1: %v", err)
	}
	if out[0].QuantityBase != 1 {
		t.Fatalf("round 0.5*1: esperaba qb=1, obtuvo %d", out[0].QuantityBase)
	}
}

// TestSaleDeductsByMeasureQuantityBase es la REGRESIÓN: una venta de un producto con
// receta capturada por medida sigue descontando correctamente leyendo quantity_base
// (deductSupplies intacto). También verifica que, tras editar la medida (recálculo en
// vivo), una nueva venta descuenta con el quantity_base recomputado.
func TestSaleDeductsByMeasureQuantityBase(t *testing.T) {
	f := setup(t)
	defer f.pool.Close()
	ctx := context.Background()

	choco, _ := f.svc.Create(ctx, f.tenantA, "Chocolate", "g", "Bolsa 1kg", 1000, nil, nil)
	scoop, _ := f.svc.CreateMeasure(ctx, f.tenantA, choco.ID, "scoop", 25)
	one := 1.0
	if _, err := f.svc.ReplaceRecipe(ctx, f.tenantA, f.prodA, []recipeItemIn{
		{SupplyID: choco.ID, MeasureID: &scoop.ID, MeasureCount: &one},
	}); err != nil {
		t.Fatalf("receta por medida: %v", err)
	}
	// Existencias iniciales: 1000 g en A1.
	pkgs := 1
	if _, _, err := f.svc.CreateMovement(ctx, f.tenantA, choco.ID, MovementInput{
		Type: "purchase", BranchID: f.branchA1, Packages: &pkgs,
	}, f.userA); err != nil {
		t.Fatalf("compra inicial: %v", err)
	}

	// Vender 2 unidades del producto: descuenta 2 * quantity_base(25) = 50 g.
	f.simulateSaleDeduct(t, f.tenantA, f.prodA, f.branchA1, 2)
	if got := f.cacheStock(t, choco.ID, f.branchA1); got != 950 {
		t.Fatalf("descuento por medida: esperaba stock 950 (1000-50), obtuvo %d", got)
	}
	// Invariante ledger == cache.
	if got, want := f.cacheStock(t, choco.ID, f.branchA1), f.sumMovements(t, choco.ID, f.branchA1); got != want {
		t.Fatalf("invariante rota: cache=%d suma=%d", got, want)
	}

	// Editar la medida a 30 g (recálculo en vivo) -> nueva venta descuenta 30 g/unidad.
	newBase := 30
	if _, err := f.svc.UpdateMeasure(ctx, f.tenantA, scoop.ID, MeasureUpdate{BaseQuantity: &newBase}); err != nil {
		t.Fatalf("editar medida: %v", err)
	}
	f.simulateSaleDeduct(t, f.tenantA, f.prodA, f.branchA1, 1) // 1 * 30 = 30
	if got := f.cacheStock(t, choco.ID, f.branchA1); got != 920 {
		t.Fatalf("descuento tras recálculo: esperaba stock 920 (950-30), obtuvo %d", got)
	}
}
