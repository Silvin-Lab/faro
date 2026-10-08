package server_test

import (
	"context"
	"net/http"
	"testing"
)

type closureResp struct {
	Closure struct {
		ID                 string `json:"id"`
		BranchID           string `json:"branchId"`
		Status             string `json:"status"`
		TotalSalesCents    *int   `json:"totalSalesCents"`
		TotalExpensesCents *int   `json:"totalExpensesCents"`
		CashExpectedCents  *int   `json:"cashExpectedCents"`
		CashCountedCents   *int   `json:"cashCountedCents"`
		CashDiffCents      *int   `json:"cashDiffCents"`
	} `json:"closure"`
}

// TestDaycloseLifecycle: open idempotente + totales en vivo, patch de efectivo contado,
// submit que congela el snapshot, 409 al re-enviar, scope por sucursal y gating de rol.
func TestDaycloseLifecycle(t *testing.T) {
	env := setupM7(t)
	defer env.close()
	ctx := context.Background()

	root := newClient(t)
	env.login(t, root, "root@faro.test", "secret123")
	b1 := env.createBranch(t, root, "Centro")
	b2 := env.createBranch(t, root, "Norte")

	// Ventas del día en b1: efectivo 10000 + tarjeta 5000. Gasto 2000.
	env.pool.Exec(ctx,
		`INSERT INTO sales (tenant_id,branch_id,total_cents,amount_paid_cents,change_cents,payment_method,created_at)
		 VALUES ($1,$2,10000,10000,0,'cash',now())`, env.tenantID, b1)
	env.pool.Exec(ctx,
		`INSERT INTO sales (tenant_id,branch_id,total_cents,amount_paid_cents,change_cents,payment_method,created_at)
		 VALUES ($1,$2,5000,5000,0,'card',now())`, env.tenantID, b1)
	env.pool.Exec(ctx,
		`INSERT INTO expenses (tenant_id,branch_id,concept_name,amount_cents,created_at)
		 VALUES ($1,$2,'Servilletas',2000,now())`, env.tenantID, b1)

	env.newBranchUser(t, root, "cajero@vanta.test", "cashier", []string{b1})
	cajero := newClient(t)
	env.login(t, cajero, "cajero@vanta.test", "secret123")

	// open (get-or-create) -> draft con totales en vivo.
	resp := env.do(t, cajero, http.MethodPost, "/dayclose/open", map[string]any{})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("open: esperaba 200, obtuvo %d", resp.StatusCode)
	}
	var c closureResp
	decode(t, resp, &c)
	id := c.Closure.ID
	if c.Closure.Status != "draft" {
		t.Fatalf("open: esperaba draft, obtuvo %s", c.Closure.Status)
	}
	if c.Closure.TotalSalesCents == nil || *c.Closure.TotalSalesCents != 15000 {
		t.Fatalf("totalSales: esperaba 15000, obtuvo %v", c.Closure.TotalSalesCents)
	}
	if c.Closure.TotalExpensesCents == nil || *c.Closure.TotalExpensesCents != 2000 {
		t.Fatalf("totalExpenses: esperaba 2000, obtuvo %v", c.Closure.TotalExpensesCents)
	}
	if c.Closure.CashExpectedCents == nil || *c.Closure.CashExpectedCents != 8000 {
		t.Fatalf("cashExpected: esperaba 8000 (10000 cash - 2000 gasto), obtuvo %v", c.Closure.CashExpectedCents)
	}

	// open otra vez => mismo id (idempotente).
	resp = env.do(t, cajero, http.MethodPost, "/dayclose/open", map[string]any{})
	var c2 closureResp
	decode(t, resp, &c2)
	if c2.Closure.ID != id {
		t.Fatalf("open idempotente: esperaba mismo id %s, obtuvo %s", id, c2.Closure.ID)
	}

	// patch efectivo contado => cashDiff en vivo.
	resp = env.do(t, cajero, http.MethodPatch, "/dayclose/"+id, map[string]any{"cashCountedCents": 7500})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("patch: esperaba 200, obtuvo %d", resp.StatusCode)
	}
	decode(t, resp, &c)
	if c.Closure.CashDiffCents == nil || *c.Closure.CashDiffCents != -500 {
		t.Fatalf("cashDiff en vivo: esperaba -500 (7500-8000), obtuvo %v", c.Closure.CashDiffCents)
	}

	// submit => snapshot congelado.
	resp = env.do(t, cajero, http.MethodPost, "/dayclose/"+id+"/submit", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("submit: esperaba 200, obtuvo %d", resp.StatusCode)
	}
	decode(t, resp, &c)
	if c.Closure.Status != "submitted" {
		t.Fatalf("submit: esperaba submitted, obtuvo %s", c.Closure.Status)
	}
	if c.Closure.CashDiffCents == nil || *c.Closure.CashDiffCents != -500 {
		t.Fatalf("submit cashDiff: esperaba -500, obtuvo %v", c.Closure.CashDiffCents)
	}
	if c.Closure.TotalSalesCents == nil || *c.Closure.TotalSalesCents != 15000 {
		t.Fatalf("submit totalSales snapshot: esperaba 15000, obtuvo %v", c.Closure.TotalSalesCents)
	}

	// re-submit => 409.
	if resp := env.do(t, cajero, http.MethodPost, "/dayclose/"+id+"/submit", nil); resp.StatusCode != http.StatusConflict {
		t.Fatalf("re-submit: esperaba 409, obtuvo %d", resp.StatusCode)
	}
	// patch sobre submitted => 409.
	if resp := env.do(t, cajero, http.MethodPatch, "/dayclose/"+id, map[string]any{"notes": "tarde"}); resp.StatusCode != http.StatusConflict {
		t.Fatalf("patch submitted: esperaba 409, obtuvo %d", resp.StatusCode)
	}

	// open tras submit => devuelve el snapshot (mismo id, submitted).
	resp = env.do(t, cajero, http.MethodPost, "/dayclose/open", map[string]any{})
	decode(t, resp, &c)
	if c.Closure.ID != id || c.Closure.Status != "submitted" {
		t.Fatalf("open tras submit: esperaba %s submitted, obtuvo %s %s", id, c.Closure.ID, c.Closure.Status)
	}

	// Scope: cajero de b2 no ve el cierre de b1.
	env.newBranchUser(t, root, "c2@vanta.test", "cashier", []string{b2})
	c2c := newClient(t)
	env.login(t, c2c, "c2@vanta.test", "secret123")
	resp = env.do(t, c2c, http.MethodGet, "/dayclose", nil)
	var list struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	decode(t, resp, &list)
	if len(list.Items) != 0 {
		t.Fatalf("scope b2: esperaba 0 cierres, obtuvo %d", len(list.Items))
	}
	// El cajero de b2 no puede ver el cierre de b1 por id (403).
	if resp := env.do(t, c2c, http.MethodGet, "/dayclose/"+id, nil); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("c2 GET cierre ajeno: esperaba 403, obtuvo %d", resp.StatusCode)
	}
	// super admin ve el cierre de b1.
	resp = env.do(t, root, http.MethodGet, "/dayclose", nil)
	decode(t, resp, &list)
	if len(list.Items) != 1 {
		t.Fatalf("super admin list: esperaba 1 cierre, obtuvo %d", len(list.Items))
	}

	// repostero => 403 al abrir.
	env.createRepostero(t, root, "repo@vanta.test")
	repo := newClient(t)
	env.login(t, repo, "repo@vanta.test", "secret123")
	if resp := env.do(t, repo, http.MethodPost, "/dayclose/open", map[string]any{}); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("repostero open: esperaba 403, obtuvo %d", resp.StatusCode)
	}
}
