-- 0030_branch_day_closures (up): corte de caja / cierre de día por sucursal (M11).
-- Aditiva. No destructiva.
--
-- Un cierre por (sucursal, fecha) (UNIQUE). Ciclo draft -> submitted: en draft los totales
-- se calculan EN VIVO desde reports (ventas/gastos del día); al submit se CONGELA el
-- snapshot (total_sales_cents, total_expenses_cents, cash_expected_cents, cash_diff_cents)
-- y ya no cambia. Liga opcionalmente el conteo de postres del día (bakery_count_id) y la
-- requisición de insumos faltantes (supply_requisition_id) que el equipo generó al cierre.
-- Montos en centavos (mismo criterio que ventas/gastos).

BEGIN;

CREATE TABLE branch_day_closures (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL REFERENCES tenants(id),
    branch_id uuid NOT NULL REFERENCES branches(id),
    closure_date date NOT NULL,
    status text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft','submitted')),
    total_sales_cents integer,
    total_expenses_cents integer,
    cash_expected_cents integer,
    cash_counted_cents integer,
    cash_diff_cents integer,
    bakery_count_id uuid REFERENCES bakery_counts(id) ON DELETE SET NULL,
    supply_requisition_id uuid REFERENCES supply_requisitions(id) ON DELETE SET NULL,
    notes text,
    created_by uuid REFERENCES users(id),
    submitted_by uuid REFERENCES users(id),
    submitted_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT branch_day_closures_unique UNIQUE (branch_id, closure_date)
);
CREATE INDEX branch_day_closures_branch_idx ON branch_day_closures (tenant_id, branch_id, closure_date);

COMMIT;
