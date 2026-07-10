-- 0014_expenses (up): módulo de gastos. Catálogo de categorías/conceptos de gasto
-- (administrado por super admin) y registro de gastos por sucursal. Aditiva: se
-- aplica ENCIMA de 0013 (ya viva). No destructiva. Reserva 0015 para insumos.

BEGIN;

-- 1. Categorías de gasto (espejo de categories sin imagen), tenant-scoped --------

CREATE TABLE IF NOT EXISTS expense_categories (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id  uuid NOT NULL REFERENCES tenants(id),
    name       text NOT NULL,
    status     text NOT NULL DEFAULT 'active', -- active | inactive
    sort_order integer NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT expense_categories_name_per_tenant UNIQUE (tenant_id, name)
);
CREATE INDEX IF NOT EXISTS expense_categories_tenant_id_idx ON expense_categories (tenant_id);

-- 2. Conceptos de gasto (agrupables por categoría, nullable = "Sin categoría") ---

CREATE TABLE IF NOT EXISTS expense_concepts (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   uuid NOT NULL REFERENCES tenants(id),
    category_id uuid REFERENCES expense_categories(id) ON DELETE SET NULL, -- nullable: sin categoría
    name        text NOT NULL,
    status      text NOT NULL DEFAULT 'active', -- active | inactive
    created_at  timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT expense_concepts_name_per_tenant UNIQUE (tenant_id, name)
);
CREATE INDEX IF NOT EXISTS expense_concepts_tenant_id_idx ON expense_concepts (tenant_id);

-- 3. Gastos registrados (branch_id NOT NULL, de la sesión; snapshot del concepto) -

CREATE TABLE IF NOT EXISTS expenses (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id    uuid NOT NULL REFERENCES tenants(id),
    branch_id    uuid NOT NULL REFERENCES branches(id),
    concept_id   uuid REFERENCES expense_concepts(id), -- snapshot en concept_name abajo
    concept_name text NOT NULL,                        -- snapshot (patrón sale_items.name)
    amount_cents integer NOT NULL CHECK (amount_cents > 0),
    created_by   uuid REFERENCES users(id),
    created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS expenses_tenant_branch_created_idx ON expenses (tenant_id, branch_id, created_at);
CREATE INDEX IF NOT EXISTS expenses_tenant_created_idx ON expenses (tenant_id, created_at);

COMMIT;
