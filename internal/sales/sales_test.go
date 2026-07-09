package sales

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// --- Unit (sin DB) ---

func TestCreateValidatesInput(t *testing.T) {
	svc := NewService(nil)
	ctx := context.Background()
	if _, err := svc.Create(ctx, "t1", nil, "cash", 100, nil, nil, nil, nil); err != ErrValidation {
		t.Fatalf("items vacíos: esperaba ErrValidation, obtuvo %v", err)
	}
	if _, err := svc.Create(ctx, "t1", []LineInput{{ProductID: "p1", Quantity: 0}}, "cash", 100, nil, nil, nil, nil); err != ErrValidation {
		t.Fatalf("cantidad 0: esperaba ErrValidation, obtuvo %v", err)
	}
	if _, err := svc.Create(ctx, "t1", []LineInput{{ProductID: "p1", Quantity: 1}}, "cheque", 100, nil, nil, nil, nil); err != ErrValidation {
		t.Fatalf("forma de pago inválida: esperaba ErrValidation, obtuvo %v", err)
	}
	if _, err := svc.Create(ctx, "t1", []LineInput{{ProductID: "p1", Quantity: 1}}, "paypal", 100, nil, nil, nil, nil); err != ErrValidation {
		t.Fatalf("paypal no aceptado: esperaba ErrValidation, obtuvo %v", err)
	}
}

// TestValidPaymentMethods documenta las formas de pago aceptadas y las que no.
func TestValidPaymentMethods(t *testing.T) {
	for _, m := range []string{"cash", "card", "transfer", "didi"} {
		if !isValidPaymentMethod(m) {
			t.Fatalf("%q debería ser una forma de pago válida", m)
		}
	}
	for _, m := range []string{"", "paypal", "cheque", "Card", "CASH"} {
		if isValidPaymentMethod(m) {
			t.Fatalf("%q no debería ser una forma de pago válida", m)
		}
	}
}

// --- Integración (DB real) ---

func testSvc(t *testing.T) (svc *Service, pool *pgxpool.Pool, a, b, prodA, prodInactive, prodB string, priceA int) {
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
	if _, err := pool.Exec(ctx, "TRUNCATE loyalty_redemptions, loyalty_promotion_products, loyalty_promotions, sale_items, sales, customers, products, categories, users, tenants RESTART IDENTITY CASCADE"); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	priceA = 4500
	pool.QueryRow(ctx, "INSERT INTO tenants (name) VALUES ('A') RETURNING id::text").Scan(&a)
	pool.QueryRow(ctx, "INSERT INTO tenants (name) VALUES ('B') RETURNING id::text").Scan(&b)
	pool.QueryRow(ctx, "INSERT INTO products (tenant_id, name, price_cents) VALUES ($1,'Latte',$2) RETURNING id::text", a, priceA).Scan(&prodA)
	pool.QueryRow(ctx, "INSERT INTO products (tenant_id, name, price_cents, status) VALUES ($1,'Viejo',1000,'inactive') RETURNING id::text", a).Scan(&prodInactive)
	pool.QueryRow(ctx, "INSERT INTO products (tenant_id, name, price_cents) VALUES ($1,'Otro',2000) RETURNING id::text", b).Scan(&prodB)
	return NewService(pool), pool, a, b, prodA, prodInactive, prodB, priceA
}

func TestCreateSaleComputesTotalAndChange(t *testing.T) {
	svc, pool, a, _, prodA, _, _, price := testSvc(t)
	defer pool.Close()

	sale, err := svc.Create(context.Background(), a, []LineInput{{ProductID: prodA, Quantity: 2}}, "cash", 10000, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("crear venta: %v", err)
	}
	if sale.TotalCents != price*2 {
		t.Fatalf("total esperaba %d, obtuvo %d", price*2, sale.TotalCents)
	}
	if sale.ChangeCents != 10000-price*2 {
		t.Fatalf("cambio esperaba %d, obtuvo %d", 10000-price*2, sale.ChangeCents)
	}
	if sale.PaymentMethod != "cash" {
		t.Fatalf("paymentMethod esperaba cash, obtuvo %s", sale.PaymentMethod)
	}
	if len(sale.Items) != 1 || sale.Items[0].UnitPriceCents != price || sale.Items[0].Quantity != 2 {
		t.Fatalf("línea incorrecta: %+v", sale.Items)
	}
}

func TestInsufficientPayment(t *testing.T) {
	svc, pool, a, _, prodA, _, _, price := testSvc(t)
	defer pool.Close()
	// paga menos que el total (price*1).
	if _, err := svc.Create(context.Background(), a, []LineInput{{ProductID: prodA, Quantity: 1}}, "cash", price-1, nil, nil, nil, nil); err != ErrInsufficientPayment {
		t.Fatalf("pago insuficiente: esperaba ErrInsufficientPayment, obtuvo %v", err)
	}
}

func TestCardPaymentSetsExactAmount(t *testing.T) {
	svc, pool, a, _, prodA, _, _, price := testSvc(t)
	defer pool.Close()
	// Con tarjeta, el monto enviado se ignora: el pagado = total y cambio = 0.
	sale, err := svc.Create(context.Background(), a, []LineInput{{ProductID: prodA, Quantity: 1}}, "card", 0, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("venta con tarjeta: %v", err)
	}
	if sale.PaymentMethod != "card" || sale.AmountPaidCents != price || sale.ChangeCents != 0 {
		t.Fatalf("tarjeta: esperaba pagado=%d cambio=0 card, obtuvo pagado=%d cambio=%d %s", price, sale.AmountPaidCents, sale.ChangeCents, sale.PaymentMethod)
	}
}

// TestExactPaymentMethodsPersist cubre las formas de pago sin cambio (transfer y
// didi, con la misma semántica que card): se aceptan, el monto pagado queda igual
// al total, el cambio es 0, y se persisten/leen bien vía Get.
func TestExactPaymentMethodsPersist(t *testing.T) {
	svc, pool, a, _, prodA, _, _, price := testSvc(t)
	defer pool.Close()
	ctx := context.Background()

	for _, method := range []string{"transfer", "didi"} {
		// amountPaidCents se ignora en pagos exactos: el pagado = total, cambio = 0.
		sale, err := svc.Create(ctx, a, []LineInput{{ProductID: prodA, Quantity: 1}}, method, 0, nil, nil, nil, nil)
		if err != nil {
			t.Fatalf("venta con %s: %v", method, err)
		}
		if sale.PaymentMethod != method || sale.AmountPaidCents != price || sale.ChangeCents != 0 {
			t.Fatalf("%s: esperaba pagado=%d cambio=0 método=%s, obtuvo pagado=%d cambio=%d método=%s",
				method, price, method, sale.AmountPaidCents, sale.ChangeCents, sale.PaymentMethod)
		}

		// Round-trip: Get devuelve el mismo método persistido.
		got, err := svc.Get(ctx, a, sale.ID)
		if err != nil {
			t.Fatalf("get venta %s: %v", method, err)
		}
		if got.PaymentMethod != method || got.AmountPaidCents != price || got.ChangeCents != 0 {
			t.Fatalf("get %s: método=%s pagado=%d cambio=%d (esperaba %s/%d/0)",
				method, got.PaymentMethod, got.AmountPaidCents, got.ChangeCents, method, price)
		}
	}
}

func TestInactiveOrForeignProductRejected(t *testing.T) {
	svc, pool, a, _, _, prodInactive, prodB, _ := testSvc(t)
	defer pool.Close()
	ctx := context.Background()
	if _, err := svc.Create(ctx, a, []LineInput{{ProductID: prodInactive, Quantity: 1}}, "cash", 100000, nil, nil, nil, nil); err != ErrInvalidProduct {
		t.Fatalf("producto inactivo: esperaba ErrInvalidProduct, obtuvo %v", err)
	}
	if _, err := svc.Create(ctx, a, []LineInput{{ProductID: prodB, Quantity: 1}}, "cash", 100000, nil, nil, nil, nil); err != ErrInvalidProduct {
		t.Fatalf("producto de otro negocio: esperaba ErrInvalidProduct, obtuvo %v", err)
	}
}

func TestGetAndIsolation(t *testing.T) {
	svc, pool, a, b, prodA, _, _, _ := testSvc(t)
	defer pool.Close()
	ctx := context.Background()

	branchA := seedBranch(t, pool, a, "Centro")
	sale, err := svc.Create(ctx, a, []LineInput{{ProductID: prodA, Quantity: 1}}, "cash", 5000, nil, nil, nil, &branchA)
	if err != nil {
		t.Fatalf("crear: %v", err)
	}

	// Negocio A obtiene su venta con líneas (ticket).
	got, err := svc.Get(ctx, a, sale.ID)
	if err != nil || len(got.Items) != 1 {
		t.Fatalf("get A: err=%v items=%d", err, len(got.Items))
	}
	// Negocio B no puede verla.
	if _, err := svc.Get(ctx, b, sale.ID); err != ErrNotFound {
		t.Fatalf("get cross-tenant: esperaba ErrNotFound, obtuvo %v", err)
	}
	// Listado acotado a la sucursal (POS): A ve su venta; B no.
	listA, _ := svc.List(ctx, a, branchA, nil, nil)
	listB, _ := svc.List(ctx, b, branchA, nil, nil)
	if len(listA) != 1 || len(listB) != 0 {
		t.Fatalf("aislamiento listado: A=%d B=%d", len(listA), len(listB))
	}
}

// seedBranch crea una sucursal del negocio y devuelve su id.
func seedBranch(t *testing.T, pool *pgxpool.Pool, tenantID, name string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO branches (tenant_id, name) VALUES ($1,$2) RETURNING id::text`, tenantID, name).Scan(&id); err != nil {
		t.Fatalf("seed branch: %v", err)
	}
	return id
}

// seedPromotion inserta una promoción activa con un producto y devuelve su id.
func seedPromotion(t *testing.T, pool *pgxpool.Pool, tenantID, productID, name string, pct, threshold int, resets bool) string {
	t.Helper()
	ctx := context.Background()
	var id string
	if err := pool.QueryRow(ctx,
		`INSERT INTO loyalty_promotions (tenant_id, name, discount_percent, visit_threshold, resets_counter)
		 VALUES ($1,$2,$3,$4,$5) RETURNING id::text`,
		tenantID, name, pct, threshold, resets).Scan(&id); err != nil {
		t.Fatalf("seed promo: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO loyalty_promotion_products (promotion_id, product_id, tenant_id) VALUES ($1,$2,$3)`,
		id, productID, tenantID); err != nil {
		t.Fatalf("seed promo product: %v", err)
	}
	return id
}

func TestSaleAppliesPromotionDiscount(t *testing.T) {
	svc, pool, a, _, prodA, _, _, price := testSvc(t)
	defer pool.Close()
	ctx := context.Background()

	// Cliente con visits=2; promoción 50% umbral 3 => aplicable (2+1 >= 3), sin reinicio.
	var cust string
	pool.QueryRow(ctx, "INSERT INTO customers (tenant_id, phone, first_name, last_name, visits) VALUES ($1,'555','Ana','Paz',2) RETURNING id::text", a).Scan(&cust)
	promoID := seedPromotion(t, pool, a, prodA, "50% Latte", 50, 3, false)

	sale, err := svc.Create(ctx, a, []LineInput{{ProductID: prodA, Quantity: 1}}, "cash", price, &cust, &promoID, nil, nil)
	if err != nil {
		t.Fatalf("venta con promo: %v", err)
	}
	wantDiscount := (price*50 + 50) / 100 // 2250
	if sale.DiscountCents != wantDiscount || sale.TotalCents != price-wantDiscount {
		t.Fatalf("descuento: total=%d discount=%d (esperaba %d/%d)", sale.TotalCents, sale.DiscountCents, price-wantDiscount, wantDiscount)
	}
	if sale.PromotionName == nil || *sale.PromotionName != "50% Latte" {
		t.Fatalf("promotionName esperaba '50%% Latte', obtuvo %v", sale.PromotionName)
	}

	var visits, lifetime int
	pool.QueryRow(ctx, "SELECT visits, visits_lifetime FROM customers WHERE id=$1", cust).Scan(&visits, &lifetime)
	if visits != 3 || lifetime != 1 {
		t.Fatalf("contadores: visits=%d lifetime=%d (esperaba 3/1)", visits, lifetime)
	}

	// Snapshot en el historial.
	var n, redemDiscount, cycleAt int
	var causedReset bool
	pool.QueryRow(ctx,
		`SELECT count(*), coalesce(max(discount_cents),0), coalesce(bool_or(caused_reset),false), coalesce(max(visits_cycle_at),0)
		   FROM loyalty_redemptions WHERE sale_id=$1`, sale.ID).Scan(&n, &redemDiscount, &causedReset, &cycleAt)
	if n != 1 || redemDiscount != wantDiscount || causedReset || cycleAt != 3 {
		t.Fatalf("redemption: n=%d discount=%d reset=%v cycleAt=%d", n, redemDiscount, causedReset, cycleAt)
	}
}

func TestSalePromotion100ResetsAndSnapshots(t *testing.T) {
	svc, pool, a, _, prodA, _, _, price := testSvc(t)
	defer pool.Close()
	ctx := context.Background()

	// Cliente con visits=2; promoción 100% (gratis) umbral 3, con reinicio.
	var cust string
	pool.QueryRow(ctx, "INSERT INTO customers (tenant_id, phone, first_name, last_name, visits) VALUES ($1,'555','Ana','Paz',2) RETURNING id::text", a).Scan(&cust)
	promoID := seedPromotion(t, pool, a, prodA, "Latte gratis", 100, 3, true)

	sale, err := svc.Create(ctx, a, []LineInput{{ProductID: prodA, Quantity: 1}}, "cash", price, &cust, &promoID, nil, nil)
	if err != nil {
		t.Fatalf("venta gratis: %v", err)
	}
	if sale.DiscountCents != price || sale.TotalCents != 0 {
		t.Fatalf("gratis: total=%d discount=%d (esperaba 0/%d)", sale.TotalCents, sale.DiscountCents, price)
	}

	// Reinicio: visits -> 0; visits_lifetime conserva el incremento.
	var visits, lifetime int
	pool.QueryRow(ctx, "SELECT visits, visits_lifetime FROM customers WHERE id=$1", cust).Scan(&visits, &lifetime)
	if visits != 0 || lifetime != 1 {
		t.Fatalf("tras reinicio: visits=%d lifetime=%d (esperaba 0/1)", visits, lifetime)
	}

	var n int
	var causedReset bool
	var cycleAt int
	pool.QueryRow(ctx,
		`SELECT count(*), coalesce(bool_or(caused_reset),false), coalesce(max(visits_cycle_at),0)
		   FROM loyalty_redemptions WHERE sale_id=$1`, sale.ID).Scan(&n, &causedReset, &cycleAt)
	if n != 1 || !causedReset || cycleAt != 3 {
		t.Fatalf("redemption: n=%d reset=%v cycleAt=%d (esperaba 1/true/3)", n, causedReset, cycleAt)
	}
}

// TestSaleDiscountRoundTrip cierra DEFECTO-001: tras una venta con promoción que
// da descuento>0, Get() y List() deben rehidratar DiscountCents y PromotionName
// (no basta con que createSale los devuelva en la respuesta inmediata).
func TestSaleDiscountRoundTrip(t *testing.T) {
	svc, pool, a, _, prodA, _, _, price := testSvc(t)
	defer pool.Close()
	ctx := context.Background()

	var cust string
	pool.QueryRow(ctx, "INSERT INTO customers (tenant_id, phone, first_name, last_name, visits) VALUES ($1,'555','Ana','Paz',2) RETURNING id::text", a).Scan(&cust)
	promoID := seedPromotion(t, pool, a, prodA, "50% Latte", 50, 3, false)
	branchA := seedBranch(t, pool, a, "Centro")

	sale, err := svc.Create(ctx, a, []LineInput{{ProductID: prodA, Quantity: 1}}, "cash", price, &cust, &promoID, nil, &branchA)
	if err != nil {
		t.Fatalf("venta con promo: %v", err)
	}
	wantDiscount := (price*50 + 50) / 100 // 2250

	// Get() rehidrata descuento y nombre de promo.
	got, err := svc.Get(ctx, a, sale.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.DiscountCents != wantDiscount {
		t.Fatalf("get discount: esperaba %d, obtuvo %d", wantDiscount, got.DiscountCents)
	}
	if got.PromotionName == nil || *got.PromotionName != "50% Latte" {
		t.Fatalf("get promotionName: esperaba '50%% Latte', obtuvo %v", got.PromotionName)
	}

	// List() también (acotada a la sucursal).
	list, err := svc.List(ctx, a, branchA, nil, nil)
	if err != nil || len(list) != 1 {
		t.Fatalf("list: err=%v len=%d", err, len(list))
	}
	if list[0].DiscountCents != wantDiscount {
		t.Fatalf("list discount: esperaba %d, obtuvo %d", wantDiscount, list[0].DiscountCents)
	}
	if list[0].PromotionName == nil || *list[0].PromotionName != "50% Latte" {
		t.Fatalf("list promotionName: esperaba '50%% Latte', obtuvo %v", list[0].PromotionName)
	}
}

// TestSalePromotionEligibleButProductNotInCart cubre la regla §3.2: si la promo
// es elegible por umbral pero ninguno de sus productos está en el carrito, la
// venta se cobra normal (discount=0, sin fila en loyalty_redemptions) y el
// contador de visitas incrementa pero NO se reinicia, aunque resets_counter=true.
func TestSalePromotionEligibleButProductNotInCart(t *testing.T) {
	svc, pool, a, _, prodA, _, _, price := testSvc(t)
	defer pool.Close()
	ctx := context.Background()

	// Otro producto del mismo negocio que NO es parte de la promoción.
	var prodOther string
	priceOther := 3000
	pool.QueryRow(ctx, "INSERT INTO products (tenant_id, name, price_cents) VALUES ($1,'Muffin',$2) RETURNING id::text", a, priceOther).Scan(&prodOther)

	var cust string
	pool.QueryRow(ctx, "INSERT INTO customers (tenant_id, phone, first_name, last_name, visits) VALUES ($1,'555','Ana','Paz',2) RETURNING id::text", a).Scan(&cust)
	// Promo sobre prodA, con reinicio; el carrito solo lleva prodOther.
	promoID := seedPromotion(t, pool, a, prodA, "Latte gratis", 100, 3, true)

	sale, err := svc.Create(ctx, a, []LineInput{{ProductID: prodOther, Quantity: 1}}, "cash", priceOther, &cust, &promoID, nil, nil)
	if err != nil {
		t.Fatalf("venta sin producto de promo: %v", err)
	}
	if sale.DiscountCents != 0 || sale.TotalCents != priceOther {
		t.Fatalf("cobro normal: total=%d discount=%d (esperaba %d/0)", sale.TotalCents, sale.DiscountCents, priceOther)
	}
	if sale.PromotionName != nil {
		t.Fatalf("promotionName debía ser nil, obtuvo %v", sale.PromotionName)
	}
	_ = price

	// visits incrementa (2 -> 3) pero NO se reinicia pese a resets_counter=true.
	var visits, lifetime int
	pool.QueryRow(ctx, "SELECT visits, visits_lifetime FROM customers WHERE id=$1", cust).Scan(&visits, &lifetime)
	if visits != 3 || lifetime != 1 {
		t.Fatalf("contadores: visits=%d lifetime=%d (esperaba 3/1, sin reinicio)", visits, lifetime)
	}

	// No hay fila en el historial de redenciones.
	var n int
	pool.QueryRow(ctx, "SELECT count(*) FROM loyalty_redemptions WHERE sale_id=$1", sale.ID).Scan(&n)
	if n != 0 {
		t.Fatalf("loyalty_redemptions: esperaba 0 filas, obtuvo %d", n)
	}
}

// TestSalePromotionChoosesHigherPricedEligibleUnit cubre chooseUnit: con varios
// productos elegibles en el carrito y sin promotionProductId, se beneficia la
// unidad elegible de mayor precio; y con promotionProductId explícito, esa.
func TestSalePromotionChoosesHigherPricedEligibleUnit(t *testing.T) {
	svc, pool, a, _, prodA, _, _, price := testSvc(t)
	defer pool.Close()
	ctx := context.Background()

	// Segundo producto elegible, más caro que prodA (price=4500).
	var prodPremium string
	pricePremium := 6000
	pool.QueryRow(ctx, "INSERT INTO products (tenant_id, name, price_cents) VALUES ($1,'Latte XL',$2) RETURNING id::text", a, pricePremium).Scan(&prodPremium)

	// Promo con dos productos elegibles (prodA y prodPremium).
	promoID := seedPromotion(t, pool, a, prodA, "10% bebida", 10, 3, false)
	if _, err := pool.Exec(ctx,
		`INSERT INTO loyalty_promotion_products (promotion_id, product_id, tenant_id) VALUES ($1,$2,$3)`,
		promoID, prodPremium, a); err != nil {
		t.Fatalf("seed segundo producto de promo: %v", err)
	}

	// Dos clientes distintos: la promo es redimible UNA vez por ciclo, así que cada
	// sub-escenario usa su propio cliente para no chocar con esa regla.
	var custAuto, custExplicit string
	pool.QueryRow(ctx, "INSERT INTO customers (tenant_id, phone, first_name, last_name, visits) VALUES ($1,'555','Ana','Paz',2) RETURNING id::text", a).Scan(&custAuto)
	pool.QueryRow(ctx, "INSERT INTO customers (tenant_id, phone, first_name, last_name, visits) VALUES ($1,'556','Bea','Ruiz',2) RETURNING id::text", a).Scan(&custExplicit)

	// Sin promotionProductId: elige el elegible de mayor precio (prodPremium).
	saleAuto, err := svc.Create(ctx, a,
		[]LineInput{{ProductID: prodA, Quantity: 1}, {ProductID: prodPremium, Quantity: 1}},
		"cash", price+pricePremium, &custAuto, &promoID, nil, nil)
	if err != nil {
		t.Fatalf("venta auto: %v", err)
	}
	wantAuto := (pricePremium*10 + 50) / 100 // 600
	if saleAuto.DiscountCents != wantAuto {
		t.Fatalf("auto elige mayor precio: discount=%d (esperaba %d)", saleAuto.DiscountCents, wantAuto)
	}

	// Con promotionProductId=prodA explícito: beneficia esa unidad aunque sea más barata.
	saleExplicit, err := svc.Create(ctx, a,
		[]LineInput{{ProductID: prodA, Quantity: 1}, {ProductID: prodPremium, Quantity: 1}},
		"cash", price+pricePremium, &custExplicit, &promoID, &prodA, nil)
	if err != nil {
		t.Fatalf("venta explícita: %v", err)
	}
	wantExplicit := (price*10 + 50) / 100 // 450
	if saleExplicit.DiscountCents != wantExplicit {
		t.Fatalf("explícito elige prodA: discount=%d (esperaba %d)", saleExplicit.DiscountCents, wantExplicit)
	}
}

// TestSalePromotionSingleRedemptionPerCycle: una promo sin reinicio ya canjeada en
// el ciclo actual no puede volver a canjearse aunque el umbral siga cumplido; el
// segundo intento devuelve 422 y no registra la venta.
func TestSalePromotionSingleRedemptionPerCycle(t *testing.T) {
	svc, pool, a, _, prodA, _, _, price := testSvc(t)
	defer pool.Close()
	ctx := context.Background()

	var cust string
	pool.QueryRow(ctx, "INSERT INTO customers (tenant_id, phone, first_name, last_name, visits) VALUES ($1,'555','Ana','Paz',2) RETURNING id::text", a).Scan(&cust)
	promoID := seedPromotion(t, pool, a, prodA, "50% Latte", 50, 3, false)

	// Primer canje: éxito (visits 2 -> 3).
	if _, err := svc.Create(ctx, a, []LineInput{{ProductID: prodA, Quantity: 1}}, "cash", price, &cust, &promoID, nil, nil); err != nil {
		t.Fatalf("primer canje: %v", err)
	}
	var visits int
	pool.QueryRow(ctx, "SELECT visits FROM customers WHERE id=$1", cust).Scan(&visits)
	if visits != 3 {
		t.Fatalf("tras primer canje visits=%d (esperaba 3)", visits)
	}

	// Segundo canje de la MISMA promo en el mismo ciclo: 422 aunque 3+1 >= 3.
	if _, err := svc.Create(ctx, a, []LineInput{{ProductID: prodA, Quantity: 1}}, "cash", price, &cust, &promoID, nil, nil); err != ErrPromotionNotEligible {
		t.Fatalf("segundo canje mismo ciclo: esperaba ErrPromotionNotEligible, obtuvo %v", err)
	}
	// La segunda venta no se registró (rollback): solo hay 1 venta y 1 redención.
	var nSales, nRedem int
	pool.QueryRow(ctx, "SELECT count(*) FROM sales WHERE tenant_id=$1", a).Scan(&nSales)
	pool.QueryRow(ctx, "SELECT count(*) FROM loyalty_redemptions WHERE customer_id=$1", cust).Scan(&nRedem)
	if nSales != 1 || nRedem != 1 {
		t.Fatalf("estado tras 422: sales=%d redemptions=%d (esperaba 1/1)", nSales, nRedem)
	}
}

// TestSalePromotionResetOpensNewCycle: tras canjear una promo con resets_counter=true
// (visits -> 0), una promo antes canjeada vuelve a estar disponible al re-alcanzar su
// umbral, porque la redención vieja pertenece al ciclo cerrado.
func TestSalePromotionResetOpensNewCycle(t *testing.T) {
	svc, pool, a, _, prodA, _, _, price := testSvc(t)
	defer pool.Close()
	ctx := context.Background()

	// Segundo producto elegible para la promo de reinicio.
	var prodB2 string
	priceB2 := 4000
	pool.QueryRow(ctx, "INSERT INTO products (tenant_id, name, price_cents) VALUES ($1,'Muffin',$2) RETURNING id::text", a, priceB2).Scan(&prodB2)

	var cust string
	pool.QueryRow(ctx, "INSERT INTO customers (tenant_id, phone, first_name, last_name, visits) VALUES ($1,'555','Ana','Paz',1) RETURNING id::text", a).Scan(&cust)

	pReg := seedPromotion(t, pool, a, prodA, "50% Latte", 50, 2, false)        // umbral 2, sin reinicio
	pReset := seedPromotion(t, pool, a, prodB2, "Muffin gratis", 100, 3, true) // umbral 3, reinicia

	// Ciclo 1: visits=1. Canjear pReg (1+1>=2) => visits 1 -> 2.
	if _, err := svc.Create(ctx, a, []LineInput{{ProductID: prodA, Quantity: 1}}, "cash", price, &cust, &pReg, nil, nil); err != nil {
		t.Fatalf("canje pReg ciclo1: %v", err)
	}
	// Reintento de pReg en el mismo ciclo => 422.
	if _, err := svc.Create(ctx, a, []LineInput{{ProductID: prodA, Quantity: 1}}, "cash", price, &cust, &pReg, nil, nil); err != ErrPromotionNotEligible {
		t.Fatalf("reintento pReg ciclo1: esperaba ErrPromotionNotEligible, obtuvo %v", err)
	}

	// Canjear pReset (visits=2, 2+1>=3) => visits 2 -> 3 -> reinicio 0.
	if _, err := svc.Create(ctx, a, []LineInput{{ProductID: prodB2, Quantity: 1}}, "cash", priceB2, &cust, &pReset, nil, nil); err != nil {
		t.Fatalf("canje pReset: %v", err)
	}
	var visits int
	pool.QueryRow(ctx, "SELECT visits FROM customers WHERE id=$1", cust).Scan(&visits)
	if visits != 0 {
		t.Fatalf("tras reinicio visits=%d (esperaba 0)", visits)
	}

	// Ciclo 2: re-alcanzar umbral de pReg. Venta sin promo => visits 0 -> 1.
	if _, err := svc.Create(ctx, a, []LineInput{{ProductID: prodA, Quantity: 1}}, "cash", price, &cust, nil, nil, nil); err != nil {
		t.Fatalf("venta sin promo ciclo2: %v", err)
	}
	// pReg vuelve a ser canjeable en el nuevo ciclo (1+1>=2, redención vieja < last_reset_at).
	sale, err := svc.Create(ctx, a, []LineInput{{ProductID: prodA, Quantity: 1}}, "cash", price, &cust, &pReg, nil, nil)
	if err != nil {
		t.Fatalf("recanje pReg ciclo2: esperaba éxito, obtuvo %v", err)
	}
	wantDiscount := (price*50 + 50) / 100
	if sale.DiscountCents != wantDiscount {
		t.Fatalf("recanje pReg ciclo2: discount=%d (esperaba %d)", sale.DiscountCents, wantDiscount)
	}
	// Debe haber 2 redenciones de pReg (una por ciclo) para este cliente.
	var nReg int
	pool.QueryRow(ctx, "SELECT count(*) FROM loyalty_redemptions WHERE customer_id=$1 AND promotion_id=$2", cust, pReg).Scan(&nReg)
	if nReg != 2 {
		t.Fatalf("redenciones de pReg: %d (esperaba 2, una por ciclo)", nReg)
	}
}

func TestSalePromotionNotEligible(t *testing.T) {
	svc, pool, a, _, prodA, _, _, price := testSvc(t)
	defer pool.Close()
	ctx := context.Background()

	// Cliente con visits=2; promoción umbral 5 => 2+1 < 5 => no elegible (422).
	var cust string
	pool.QueryRow(ctx, "INSERT INTO customers (tenant_id, phone, first_name, last_name, visits) VALUES ($1,'555','Ana','Paz',2) RETURNING id::text", a).Scan(&cust)
	promoID := seedPromotion(t, pool, a, prodA, "50% Latte", 50, 5, false)

	if _, err := svc.Create(ctx, a, []LineInput{{ProductID: prodA, Quantity: 1}}, "cash", price, &cust, &promoID, nil, nil); err != ErrPromotionNotEligible {
		t.Fatalf("promo no elegible: esperaba ErrPromotionNotEligible, obtuvo %v", err)
	}
	// La venta no se registró (rollback).
	var sales int
	pool.QueryRow(ctx, "SELECT count(*) FROM sales WHERE tenant_id=$1", a).Scan(&sales)
	if sales != 0 {
		t.Fatalf("no debía registrarse venta, obtuvo %d", sales)
	}
}
