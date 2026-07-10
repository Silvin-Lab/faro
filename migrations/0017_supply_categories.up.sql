-- 0017_supply_categories (up): categorías de insumo (tipo de insumo), administrables
-- por super admin, espejo de expense_categories. Cada insumo puede tener una categoría
-- OPCIONAL (category_id NULLABLE). Aditiva: se aplica ENCIMA de 0016 (ya viva). No
-- destructiva.

BEGIN;

-- 1. Categorías de insumo (espejo de expense_categories), tenant-scoped -----------
CREATE TABLE IF NOT EXISTS supply_categories (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id  uuid NOT NULL REFERENCES tenants(id),
    name       text NOT NULL,
    status     text NOT NULL DEFAULT 'active', -- active | inactive
    sort_order integer NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT supply_categories_name_per_tenant UNIQUE (tenant_id, name)
);
CREATE INDEX IF NOT EXISTS supply_categories_tenant_id_idx ON supply_categories (tenant_id);

-- 2. El insumo gana una categoría OPCIONAL. NULLABLE a propósito: un insumo puede
--    no tener categoría. ON DELETE SET NULL: borrar la categoría deja los insumos sin
--    categoría (no borra insumos).
ALTER TABLE supplies ADD COLUMN IF NOT EXISTS category_id uuid REFERENCES supply_categories(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS supplies_category_id_idx ON supplies (category_id);

COMMIT;
