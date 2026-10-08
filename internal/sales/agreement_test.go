package sales

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// --- Descuentos de convenio y atribución de usuario (M12, ADR-010) ---
//
// Regresión de la transacción de venta: convenio (% sobre el subtotal restante tras
// la lealtad, half-up, acotado), combinación con lealtad, los 4 métodos de pago,
// rollback ante descuento no elegible, deductSupplies intacto y user_id de sesión.

// seedAgreementDiscount inserta un descuento de convenio del negocio y devuelve su id.
func seedAgreementDiscount(t *testing.T, pool *pgxpool.Pool, tenantID string, percent int, status string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO agreement_discounts (tenant_id, percent, status) VALUES ($1,$2,$3) RETURNING id::text`,
		tenantID, percent, status).Scan(&id); err != nil {
		t.Fatalf("seed agreement discount: %v", err)
	}
	return id
}

// seedUser inserta un usuario del negocio (para la atribución de la venta) y
// devuelve su id y nombre.
func seedUser(t *testing.T, pool *pgxpool.Pool, tenantID, email, name string) (string, string) {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO users (tenant_id, email, password_hash, name) VALUES ($1,$2,'x',$3) RETURNING id::text`,
		tenantID, email, name).Scan(&id); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return id, name
}

// seedCustomer inserta un cliente del negocio con visitas iniciales.
func seedCustomer(t *testing.T, pool *pgxpool.Pool, tenantID, phone string, visits int) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO customers (tenant_id, phone, first_name, last_name, visits) VALUES ($1,$2,'Ana','Paz',$3) RETURNING id::text`,
		tenantID, phone, visits).Scan(&id); err != nil {
		t.Fatalf("seed customer: %v", err)
	}
	return id
}

// TestSaleRecordsUserAndNoAgreement: venta normal (sin cliente, sin descuentos) con
// user_id de la sesión. Convenio en 0/null; soldBy resuelto; Get rehidrata todo.
func TestSaleRecordsUserAndNoAgreement(t *testing.T) {
	svc, pool, a, _, prodA, _, _, price := testSvc(t)
	defer pool.Close()
	ctx := context.Background()

	userID, userName := seedUser(t, pool, a, "caja@faro.test", "Caja Uno")

	sale, err := svc.Create(ctx, a, []LineInput{{ProductID: prodA, Quantity: 1}}, "cash", price, nil, nil, nil, nil, nil, &userID)
	if err != nil {
		t.Fatalf("venta normal: %v", err)
	}
	if sale.TotalCents != price || sale.DiscountCents != 0 || sale.AgreementDiscountCents != 0 || sale.AgreementDiscountID != nil || sale.AgreementDiscountPercent != nil {
		t.Fatalf("venta normal con convenio esperaba 0/null: %+v", sale)
	}
	if sale.SoldByUserID == nil || *sale.SoldByUserID != userID || sale.SoldByName == nil || *sale.SoldByName != userName {
		t.Fatalf("atribución: soldBy=%v name=%v (esperaba %s/%s)", sale.SoldByUserID, sale.SoldByName, userID, userName)
	}

	got, err := svc.Get(ctx, a, sale.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.SoldByName == nil || *got.SoldByName != userName || got.AgreementDiscountCents != 0 {
		t.Fatalf("get rehidrata mal: soldBy=%v agreement=%d", got.SoldByName, got.AgreementDiscountCents)
	}
}

// TestSaleHistoricalUserNull: venta sin user_id (histórica) => soldBy null ("Sin
// registro" en el cliente).
func TestSaleHistoricalUserNull(t *testing.T) {
	svc, pool, a, _, prodA, _, _, price := testSvc(t)
	defer pool.Close()
	ctx := context.Background()

	sale, err := svc.Create(ctx, a, []LineInput{{ProductID: prodA, Quantity: 1}}, "cash", price, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("venta: %v", err)
	}
	if sale.SoldByUserID != nil || sale.SoldByName != nil {
		t.Fatalf("sin user_id esperaba null: soldBy=%v name=%v", sale.SoldByUserID, sale.SoldByName)
	}
}

// TestSaleOnlyAgreement: convenio sin lealtad. total = subtotal - round(subtotal*pct/100).
func TestSaleOnlyAgreement(t *testing.T) {
	svc, pool, a, _, prodA, _, _, price := testSvc(t)
	defer pool.Close()
	ctx := context.Background()

	cust := seedCustomer(t, pool, a, "555", 0)
	adID := seedAgreementDiscount(t, pool, a, 10, "active")

	sale, err := svc.Create(ctx, a, []LineInput{{ProductID: prodA, Quantity: 1}}, "cash", price, &cust, nil, nil, nil, &adID, nil)
	if err != nil {
		t.Fatalf("venta convenio: %v", err)
	}
	wantAgreement := (price*10 + 50) / 100 // 4500 -> 450
	if sale.AgreementDiscountCents != wantAgreement || sale.DiscountCents != 0 {
		t.Fatalf("convenio: agreement=%d discount=%d (esperaba %d/0)", sale.AgreementDiscountCents, sale.DiscountCents, wantAgreement)
	}
	if sale.TotalCents != price-wantAgreement {
		t.Fatalf("total: %d (esperaba %d)", sale.TotalCents, price-wantAgreement)
	}
	if sale.AgreementDiscountPercent == nil || *sale.AgreementDiscountPercent != 10 || sale.AgreementDiscountID == nil || *sale.AgreementDiscountID != adID {
		t.Fatalf("snapshot convenio: pct=%v id=%v", sale.AgreementDiscountPercent, sale.AgreementDiscountID)
	}

	// Round-trip por Get.
	got, err := svc.Get(ctx, a, sale.ID)
	if err != nil || got.AgreementDiscountCents != wantAgreement || got.AgreementDiscountPercent == nil || *got.AgreementDiscountPercent != 10 {
		t.Fatalf("get convenio: err=%v agreement=%d pct=%v", err, got.AgreementDiscountCents, got.AgreementDiscountPercent)
	}
}

// TestSaleLoyaltyPlusAgreement: lealtad primero, convenio sobre el remanente,
// half-up, cuadra al centavo. Varios % para ejercitar el redondeo.
func TestSaleLoyaltyPlusAgreement(t *testing.T) {
	svc, pool, a, _, prodA, _, _, price := testSvc(t)
	defer pool.Close()
	ctx := context.Background()

	cases := []struct {
		name      string
		agPct     int
		wantAgree int
	}{
		// loyalty 50% sobre 4500 => 2250; remaining 2250.
		{"10pct", 10, (2250*10 + 50) / 100}, // 225
		{"15pct", 15, (2250*15 + 50) / 100}, // 337.5 -> 338 (half-up)
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cust := seedCustomer(t, pool, a, "55"+tc.name, 2)
			promoID := seedPromotion(t, pool, a, prodA, "50% Latte", 50, 3, false)
			adID := seedAgreementDiscount(t, pool, a, tc.agPct, "active")

			sale, err := svc.Create(ctx, a, []LineInput{{ProductID: prodA, Quantity: 1}}, "cash", price, &cust, &promoID, nil, nil, &adID, nil)
			if err != nil {
				t.Fatalf("venta lealtad+convenio: %v", err)
			}
			wantLoyalty := (price*50 + 50) / 100 // 2250
			if sale.DiscountCents != wantLoyalty {
				t.Fatalf("lealtad: %d (esperaba %d)", sale.DiscountCents, wantLoyalty)
			}
			if sale.AgreementDiscountCents != tc.wantAgree {
				t.Fatalf("convenio: %d (esperaba %d)", sale.AgreementDiscountCents, tc.wantAgree)
			}
			if sale.TotalCents != price-wantLoyalty-tc.wantAgree {
				t.Fatalf("total: %d (esperaba %d) — no cuadra al centavo", sale.TotalCents, price-wantLoyalty-tc.wantAgree)
			}
		})
	}
}

// TestSaleAgreementWithoutCustomerRejected: agreementDiscountId sin cliente => 422
// (gate del orquestador); la venta NO se registra.
func TestSaleAgreementWithoutCustomerRejected(t *testing.T) {
	svc, pool, a, _, prodA, _, _, price := testSvc(t)
	defer pool.Close()
	ctx := context.Background()

	adID := seedAgreementDiscount(t, pool, a, 10, "active")
	if _, err := svc.Create(ctx, a, []LineInput{{ProductID: prodA, Quantity: 1}}, "cash", price, nil, nil, nil, nil, &adID, nil); err != ErrAgreementNotEligible {
		t.Fatalf("convenio sin cliente: esperaba ErrAgreementNotEligible, obtuvo %v", err)
	}
	var n int
	pool.QueryRow(ctx, "SELECT count(*) FROM sales WHERE tenant_id=$1", a).Scan(&n)
	if n != 0 {
		t.Fatalf("no debía registrarse venta, obtuvo %d", n)
	}
}

// TestSaleAgreementInactiveOrForeignRejected: descuento inactivo, de otro tenant o
// inexistente => 422 y rollback (la venta no se crea).
func TestSaleAgreementInactiveOrForeignRejected(t *testing.T) {
	svc, pool, a, b, prodA, _, _, price := testSvc(t)
	defer pool.Close()
	ctx := context.Background()

	cust := seedCustomer(t, pool, a, "555", 0)
	inactive := seedAgreementDiscount(t, pool, a, 20, "inactive")
	foreign := seedAgreementDiscount(t, pool, b, 10, "active")
	nonexistent := "00000000-0000-0000-0000-000000000000"

	for _, tc := range []struct {
		name string
		id   string
	}{
		{"inactivo", inactive},
		{"otro tenant", foreign},
		{"inexistente", nonexistent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := svc.Create(ctx, a, []LineInput{{ProductID: prodA, Quantity: 1}}, "cash", price, &cust, nil, nil, nil, &tc.id, nil); err != ErrAgreementNotEligible {
				t.Fatalf("%s: esperaba ErrAgreementNotEligible, obtuvo %v", tc.name, err)
			}
		})
	}
	var n int
	pool.QueryRow(ctx, "SELECT count(*) FROM sales WHERE tenant_id=$1", a).Scan(&n)
	if n != 0 {
		t.Fatalf("ninguna venta debía registrarse, obtuvo %d", n)
	}
}

// TestSaleAgreementAllPaymentMethods: los 4 métodos con convenio. No-cash cobran el
// total neto exacto (cambio 0); cash valida y calcula cambio; insuficiente => 422/error.
func TestSaleAgreementAllPaymentMethods(t *testing.T) {
	svc, pool, a, _, prodA, _, _, price := testSvc(t)
	defer pool.Close()
	ctx := context.Background()

	adID := seedAgreementDiscount(t, pool, a, 10, "active")
	wantAgreement := (price*10 + 50) / 100 // 450
	total := price - wantAgreement         // 4050

	for _, method := range []string{"card", "transfer", "didi"} {
		cust := seedCustomer(t, pool, a, "nc"+method, 0)
		sale, err := svc.Create(ctx, a, []LineInput{{ProductID: prodA, Quantity: 1}}, method, 0, &cust, nil, nil, nil, &adID, nil)
		if err != nil {
			t.Fatalf("%s con convenio: %v", method, err)
		}
		if sale.TotalCents != total || sale.AmountPaidCents != total || sale.ChangeCents != 0 {
			t.Fatalf("%s: total=%d pagado=%d cambio=%d (esperaba %d/%d/0)", method, sale.TotalCents, sale.AmountPaidCents, sale.ChangeCents, total, total)
		}
	}

	// Efectivo: cambio sobre el total neto.
	custCash := seedCustomer(t, pool, a, "cash", 0)
	sale, err := svc.Create(ctx, a, []LineInput{{ProductID: prodA, Quantity: 1}}, "cash", 5000, &custCash, nil, nil, nil, &adID, nil)
	if err != nil {
		t.Fatalf("cash con convenio: %v", err)
	}
	if sale.TotalCents != total || sale.ChangeCents != 5000-total {
		t.Fatalf("cash: total=%d cambio=%d (esperaba %d/%d)", sale.TotalCents, sale.ChangeCents, total, 5000-total)
	}

	// Efectivo insuficiente: paga menos que el total neto => error, sin registrar.
	custShort := seedCustomer(t, pool, a, "short", 0)
	if _, err := svc.Create(ctx, a, []LineInput{{ProductID: prodA, Quantity: 1}}, "cash", total-1, &custShort, nil, nil, nil, &adID, nil); err != ErrInsufficientPayment {
		t.Fatalf("cash insuficiente: esperaba ErrInsufficientPayment, obtuvo %v", err)
	}
}

// TestSaleAgreementDeductSuppliesUnaffected: con convenio aplicado, el movimiento de
// insumos y el stock resultante son idénticos a la misma venta sin convenio (el
// descuento no toca inventario).
func TestSaleAgreementDeductSuppliesUnaffected(t *testing.T) {
	svc, pool, a, _, prodA, _, _, price := testSvc(t)
	defer pool.Close()
	ctx := context.Background()

	branchA := seedBranch(t, pool, a, "Centro")
	milk := seedSupply(t, pool, a, "Leche", "ml", 900)
	seedRecipe(t, pool, a, prodA, milk, 200)
	seedStock(t, pool, a, milk, branchA, 5000)

	cust := seedCustomer(t, pool, a, "555", 0)
	adID := seedAgreementDiscount(t, pool, a, 10, "active")

	sale, err := svc.Create(ctx, a, []LineInput{{ProductID: prodA, Quantity: 2}}, "cash", price*2, &cust, nil, nil, &branchA, &adID, nil)
	if err != nil {
		t.Fatalf("venta con convenio: %v", err)
	}
	if sale.AgreementDiscountCents == 0 {
		t.Fatalf("el convenio debía aplicarse (>0)")
	}
	// 1 movimiento 'sale' de -(200×2); stock 5000 - 400 = 4600.
	var nMov, movQty int
	pool.QueryRow(ctx, "SELECT count(*), coalesce(sum(quantity_base),0) FROM supply_movements WHERE sale_id=$1", sale.ID).Scan(&nMov, &movQty)
	if nMov != 1 || movQty != -400 {
		t.Fatalf("movimientos: n=%d qty=%d (esperaba 1/-400)", nMov, movQty)
	}
	if got := stockOf(t, pool, milk, branchA); got != 4600 {
		t.Fatalf("stock: %d (esperaba 4600, el convenio no toca inventario)", got)
	}
}
