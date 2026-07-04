package sales

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"faro/internal/dberr"
)

var (
	ErrNotFound             = errors.New("not found")
	ErrInvalidProduct       = errors.New("invalid product")
	ErrInsufficientPayment  = errors.New("insufficient payment")
	ErrInvalidCustomer      = errors.New("invalid customer")
	ErrPromotionNotEligible = errors.New("promotion not eligible")
)

type store struct {
	pool *pgxpool.Pool
}

func newStore(pool *pgxpool.Pool) *store {
	return &store{pool: pool}
}

type computedLine struct {
	productID string
	name      string
	unitCents int
	quantity  int
	lineCents int
}

// promotion es la promoción cargada para aplicar a una venta.
type promotion struct {
	name           string
	discountPct    int
	visitThreshold int
	resetsCounter  bool
	productIDs     map[string]bool
}

// createSale registra la venta en una transacción. El total y el descuento de
// lealtad se calculan con los precios de los productos del negocio (no se confía
// en el cliente). Si se aplica una promoción, escribe el snapshot en
// loyalty_redemptions e incrementa/reinicia el contador de visitas.
func (s *store) createSale(ctx context.Context, tenantID string, items []LineInput, paymentMethod string, amountPaidCents int, customerID, promotionID, promotionProductID, branchID *string) (Sale, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Sale{}, err
	}
	defer tx.Rollback(ctx)

	// Validar el cliente (si se asocia): nombre para el ticket y visitas para la
	// elegibilidad de la promoción (sobre el contador almacenado, pre-venta).
	var customerName *string
	var customerVisits int
	if customerID != nil {
		var fn, ln string
		err := tx.QueryRow(ctx,
			`SELECT first_name, last_name, visits FROM customers WHERE id = $1 AND tenant_id = $2`,
			*customerID, tenantID).Scan(&fn, &ln, &customerVisits)
		switch {
		case errors.Is(err, pgx.ErrNoRows), dberr.IsInvalidText(err):
			return Sale{}, ErrInvalidCustomer
		case err != nil:
			return Sale{}, err
		}
		full := fn + " " + ln
		customerName = &full
	}

	var lines []computedLine
	total := 0
	for _, it := range items {
		var name, status string
		var price int
		err := tx.QueryRow(ctx,
			`SELECT name, price_cents, status FROM products WHERE id = $1 AND tenant_id = $2`,
			it.ProductID, tenantID).Scan(&name, &price, &status)
		switch {
		case errors.Is(err, pgx.ErrNoRows), dberr.IsInvalidText(err):
			return Sale{}, ErrInvalidProduct
		case err != nil:
			return Sale{}, err
		}
		if status != "active" {
			return Sale{}, ErrInvalidProduct
		}
		lt := price * it.Quantity
		lines = append(lines, computedLine{it.ProductID, name, price, it.Quantity, lt})
		total += lt
	}
	subtotal := total

	// Promoción (opcional): calcula el descuento de UNA unidad de un producto
	// elegible presente en el carrito. Requiere cliente.
	discountCents := 0
	var appliedPromo *promotion
	if promotionID != nil && customerID != nil {
		promo, err := loadPromotion(ctx, tx, tenantID, *promotionID)
		switch {
		case errors.Is(err, pgx.ErrNoRows), dberr.IsInvalidText(err):
			return Sale{}, ErrPromotionNotEligible
		case err != nil:
			return Sale{}, err
		}
		// Elegibilidad: la venta en curso cuenta para su propio umbral.
		if customerVisits+1 < promo.visitThreshold {
			return Sale{}, ErrPromotionNotEligible
		}
		// Una promoción es redimible UNA sola vez por ciclo: rechazar si ya se
		// canjeó en el ciclo actual (created_at > last_reset_at, donde last_reset_at
		// = MAX(created_at) de las redenciones que reiniciaron el contador). Esto
		// evita la doble redención por API dentro del mismo ciclo.
		var redeemedThisCycle bool
		if err := tx.QueryRow(ctx,
			`SELECT EXISTS (
			   SELECT 1 FROM loyalty_redemptions
			    WHERE tenant_id = $1 AND customer_id = $2 AND promotion_id = $3
			      AND created_at > COALESCE(
			            (SELECT MAX(created_at) FROM loyalty_redemptions
			              WHERE tenant_id = $1 AND customer_id = $2 AND caused_reset = true),
			            'epoch'::timestamptz))`,
			tenantID, *customerID, *promotionID).Scan(&redeemedThisCycle); err != nil {
			return Sale{}, err
		}
		if redeemedThisCycle {
			return Sale{}, ErrPromotionNotEligible
		}
		// Elegir la unidad beneficiada: promotionProductId si es elegible y está en
		// el carrito; si no, el producto elegible de mayor precio en el carrito.
		chosen := chooseUnit(lines, promo, promotionProductID)
		if chosen != nil {
			d := (chosen.unitCents*promo.discountPct + 50) / 100 // round
			if d < 0 {
				d = 0
			}
			if d > subtotal {
				d = subtotal
			}
			discountCents = d
			if discountCents > 0 {
				appliedPromo = &promo
			}
		}
		// Si ningún producto de la promo está en el carrito (chosen == nil) o el
		// descuento resulta 0: se cobra normal, sin historial ni reinicio.
	}

	total = subtotal - discountCents

	// Tarjeta: el monto pagado es exactamente el total (sin cambio).
	// Efectivo: se valida que alcance y se calcula el cambio.
	var amountPaid, change int
	if paymentMethod == "card" {
		amountPaid = total
		change = 0
	} else {
		if amountPaidCents < total {
			return Sale{}, ErrInsufficientPayment
		}
		amountPaid = amountPaidCents
		change = amountPaidCents - total
	}

	var sale Sale
	if err := tx.QueryRow(ctx,
		`INSERT INTO sales (tenant_id, total_cents, amount_paid_cents, change_cents, payment_method, customer_id, discount_cents, branch_id)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		 RETURNING id::text, tenant_id::text, total_cents, amount_paid_cents, change_cents, payment_method, customer_id::text, discount_cents, branch_id::text, created_at`,
		tenantID, total, amountPaid, change, paymentMethod, customerID, discountCents, branchID).
		Scan(&sale.ID, &sale.TenantID, &sale.TotalCents, &sale.AmountPaidCents, &sale.ChangeCents, &sale.PaymentMethod, &sale.CustomerID, &sale.DiscountCents, &sale.BranchID, &sale.CreatedAt); err != nil {
		return Sale{}, err
	}
	sale.CustomerName = customerName

	// Nombre de la sucursal (informativo) para la respuesta de la venta.
	if sale.BranchID != nil {
		var name string
		if err := tx.QueryRow(ctx,
			`SELECT name FROM branches WHERE id = $1 AND tenant_id = $2`, *sale.BranchID, tenantID).Scan(&name); err != nil {
			return Sale{}, err
		}
		sale.BranchName = &name
	}

	for _, l := range lines {
		var item SaleItem
		if err := tx.QueryRow(ctx,
			`INSERT INTO sale_items (sale_id, product_id, name, unit_price_cents, quantity, line_total_cents)
			 VALUES ($1, $2, $3, $4, $5, $6)
			 RETURNING id::text, product_id::text, name, unit_price_cents, quantity, line_total_cents`,
			sale.ID, l.productID, l.name, l.unitCents, l.quantity, l.lineCents).
			Scan(&item.ID, &item.ProductID, &item.Name, &item.UnitPriceCents, &item.Quantity, &item.LineTotalCents); err != nil {
			return Sale{}, err
		}
		sale.Items = append(sale.Items, item)
	}

	// Lealtad: cada venta con cliente incrementa el contador de ciclo y el de por
	// vida. Si se aplicó una promoción con descuento, se escribe el snapshot y —si
	// la promoción reinicia— el contador de ciclo vuelve a 0.
	if customerID != nil {
		var newVisits, newLifetime int
		if err := tx.QueryRow(ctx,
			`UPDATE customers SET visits = visits + 1, visits_lifetime = visits_lifetime + 1
			  WHERE id = $1 AND tenant_id = $2 RETURNING visits, visits_lifetime`,
			*customerID, tenantID).Scan(&newVisits, &newLifetime); err != nil {
			return Sale{}, err
		}

		if appliedPromo != nil {
			if _, err := tx.Exec(ctx,
				`INSERT INTO loyalty_redemptions
				   (tenant_id, customer_id, sale_id, promotion_id, promotion_name, discount_percent,
				    visit_threshold, caused_reset, visits_cycle_at, visits_lifetime_at, discount_cents)
				 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
				tenantID, *customerID, sale.ID, *promotionID, appliedPromo.name, appliedPromo.discountPct,
				appliedPromo.visitThreshold, appliedPromo.resetsCounter, newVisits, newLifetime, discountCents); err != nil {
				return Sale{}, err
			}
			if appliedPromo.resetsCounter {
				if _, err := tx.Exec(ctx,
					`UPDATE customers SET visits = 0 WHERE id = $1 AND tenant_id = $2`,
					*customerID, tenantID); err != nil {
					return Sale{}, err
				}
			}
			sale.PromotionName = &appliedPromo.name
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return Sale{}, err
	}
	return sale, nil
}

// loadPromotion carga una promoción activa del negocio con su set de productos.
func loadPromotion(ctx context.Context, tx pgx.Tx, tenantID, promotionID string) (promotion, error) {
	var p promotion
	err := tx.QueryRow(ctx,
		`SELECT name, discount_percent, visit_threshold, resets_counter
		   FROM loyalty_promotions WHERE id = $1 AND tenant_id = $2 AND status = 'active'`,
		promotionID, tenantID).Scan(&p.name, &p.discountPct, &p.visitThreshold, &p.resetsCounter)
	if err != nil {
		return promotion{}, err
	}
	rows, err := tx.Query(ctx,
		`SELECT product_id::text FROM loyalty_promotion_products
		  WHERE promotion_id = $1 AND tenant_id = $2`, promotionID, tenantID)
	if err != nil {
		return promotion{}, err
	}
	defer rows.Close()
	p.productIDs = map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return promotion{}, err
		}
		p.productIDs[id] = true
	}
	return p, rows.Err()
}

// chooseUnit selecciona la línea del carrito beneficiada por la promoción:
// promotionProductID si es elegible y está en el carrito; si no, el producto
// elegible de mayor precio unitario en el carrito. nil si ninguno aplica.
func chooseUnit(lines []computedLine, p promotion, promotionProductID *string) *computedLine {
	if promotionProductID != nil && p.productIDs[*promotionProductID] {
		for i := range lines {
			if lines[i].productID == *promotionProductID {
				return &lines[i]
			}
		}
	}
	var best *computedLine
	for i := range lines {
		if !p.productIDs[lines[i].productID] {
			continue
		}
		if best == nil || lines[i].unitCents > best.unitCents {
			best = &lines[i]
		}
	}
	return best
}

// listByTenant lista ventas del negocio. Si se da rango [from, to) filtra por
// fecha (para "ventas del día"); si no, devuelve las últimas 50.
func (s *store) listByTenant(ctx context.Context, tenantID, branchID string, from, to *time.Time) ([]Sale, error) {
	const base = `SELECT s.id::text, s.tenant_id::text, s.total_cents, s.amount_paid_cents, s.change_cents,
		        s.payment_method, s.customer_id::text, (cu.first_name || ' ' || cu.last_name), s.discount_cents, lr.promotion_name,
		        s.branch_id::text, b.name, s.created_at
		   FROM sales s
		   LEFT JOIN customers cu ON cu.id = s.customer_id
		   LEFT JOIN loyalty_redemptions lr ON lr.sale_id = s.id
		   LEFT JOIN branches b ON b.id = s.branch_id `

	var rows pgx.Rows
	var err error
	if from != nil && to != nil {
		rows, err = s.pool.Query(ctx, base+
			`WHERE s.tenant_id = $1 AND s.branch_id = $2 AND s.created_at >= $3 AND s.created_at < $4
			 ORDER BY s.created_at DESC LIMIT 500`, tenantID, branchID, *from, *to)
	} else {
		rows, err = s.pool.Query(ctx, base+
			`WHERE s.tenant_id = $1 AND s.branch_id = $2 ORDER BY s.created_at DESC LIMIT 50`, tenantID, branchID)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Sale
	for rows.Next() {
		var sale Sale
		if err := rows.Scan(&sale.ID, &sale.TenantID, &sale.TotalCents, &sale.AmountPaidCents, &sale.ChangeCents, &sale.PaymentMethod, &sale.CustomerID, &sale.CustomerName, &sale.DiscountCents, &sale.PromotionName, &sale.BranchID, &sale.BranchName, &sale.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, sale)
	}
	return out, rows.Err()
}

func (s *store) get(ctx context.Context, tenantID, id string) (Sale, error) {
	var sale Sale
	err := s.pool.QueryRow(ctx,
		`SELECT s.id::text, s.tenant_id::text, s.total_cents, s.amount_paid_cents, s.change_cents,
		        s.payment_method, s.customer_id::text, (cu.first_name || ' ' || cu.last_name), s.discount_cents, lr.promotion_name,
		        s.branch_id::text, b.name, s.created_at
		   FROM sales s
		   LEFT JOIN customers cu ON cu.id = s.customer_id
		   LEFT JOIN loyalty_redemptions lr ON lr.sale_id = s.id
		   LEFT JOIN branches b ON b.id = s.branch_id
		  WHERE s.id = $1 AND s.tenant_id = $2`, id, tenantID).
		Scan(&sale.ID, &sale.TenantID, &sale.TotalCents, &sale.AmountPaidCents, &sale.ChangeCents, &sale.PaymentMethod, &sale.CustomerID, &sale.CustomerName, &sale.DiscountCents, &sale.PromotionName, &sale.BranchID, &sale.BranchName, &sale.CreatedAt)
	switch {
	case errors.Is(err, pgx.ErrNoRows), dberr.IsInvalidText(err):
		return Sale{}, ErrNotFound
	case err != nil:
		return Sale{}, err
	}

	rows, err := s.pool.Query(ctx,
		`SELECT id::text, product_id::text, name, unit_price_cents, quantity, line_total_cents
		   FROM sale_items WHERE sale_id = $1 ORDER BY id`, sale.ID)
	if err != nil {
		return Sale{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var item SaleItem
		if err := rows.Scan(&item.ID, &item.ProductID, &item.Name, &item.UnitPriceCents, &item.Quantity, &item.LineTotalCents); err != nil {
			return Sale{}, err
		}
		sale.Items = append(sale.Items, item)
	}
	return sale, rows.Err()
}
