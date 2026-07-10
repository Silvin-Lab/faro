-- 0015_supplies (up): módulo de insumos. Catálogo de insumos (administrado por
-- super admin), recetas globales por producto e inventario POR SUCURSAL con un
-- ledger de movimientos como fuente de verdad y un cache de existencias. Aditiva:
-- se aplica ENCIMA de 0014 (ya viva). No destructiva. Ver plan Gastos/Insumos.
--
-- Decisiones (cerradas con el humano): una sola presentación por insumo; el
-- inventario es un registro pasivo (NADA bloqueante) -> el stock PUEDE quedar
-- negativo (sin CHECK de no-negatividad); recetas globales, existencias por
-- sucursal. El tipo de movimiento 'sale' lo usará una fase posterior (descuento
-- automático en la venta); se incluye en el CHECK desde ahora.

BEGIN;

-- 1. Insumos (catálogo, tenant-scoped). base_unit es INMUTABLE post-creación
--    (validado en la capa service). package_content = contenido de UNA presentación
--    en unidad base (ej. "Bote 900 ml" -> 900). Sin columna de stock: vive por
--    sucursal en supply_branch_stock.
CREATE TABLE IF NOT EXISTS supplies (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       uuid NOT NULL REFERENCES tenants(id),
    name            text NOT NULL,
    base_unit       text NOT NULL CHECK (base_unit IN ('g','ml','pieza')),
    package_name    text NOT NULL,
    package_content integer NOT NULL CHECK (package_content > 0), -- en unidad base
    status          text NOT NULL DEFAULT 'active', -- active | inactive
    created_at      timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT supplies_name_per_tenant UNIQUE (tenant_id, name)
);
CREATE INDEX IF NOT EXISTS supplies_tenant_id_idx ON supplies (tenant_id);

-- 2. Recetas (product_supplies, GLOBALES = no dependen de sucursal). quantity_base
--    = consumo por unidad vendida del producto, en unidad base del insumo.
CREATE TABLE IF NOT EXISTS product_supplies (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id     uuid NOT NULL REFERENCES tenants(id),
    product_id    uuid NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    supply_id     uuid NOT NULL REFERENCES supplies(id) ON DELETE CASCADE,
    quantity_base integer NOT NULL CHECK (quantity_base > 0),
    created_at    timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT product_supplies_unique UNIQUE (tenant_id, product_id, supply_id)
);
CREATE INDEX IF NOT EXISTS product_supplies_product_id_idx ON product_supplies (product_id);
CREATE INDEX IF NOT EXISTS product_supplies_supply_id_idx ON product_supplies (supply_id);

-- 3. Cache de existencias por sucursal. stock_base PUEDE ser negativo (nada
--    bloqueante): merma/cortesía sin existencias y ventas sobre stock cero lo
--    dejan negativo a propósito. El cache SIEMPRE se actualiza en la misma
--    transacción que el movimiento del ledger (sin ventana de inconsistencia).
CREATE TABLE IF NOT EXISTS supply_branch_stock (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id  uuid NOT NULL REFERENCES tenants(id),
    supply_id  uuid NOT NULL REFERENCES supplies(id) ON DELETE CASCADE,
    branch_id  uuid NOT NULL REFERENCES branches(id),
    stock_base integer NOT NULL DEFAULT 0, -- SIN CHECK: puede ser negativo
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT supply_branch_stock_unique UNIQUE (supply_id, branch_id)
);
CREATE INDEX IF NOT EXISTS supply_branch_stock_tenant_branch_idx ON supply_branch_stock (tenant_id, branch_id);

-- 4. Ledger de movimientos (fuente de verdad). quantity_base FIRMADO: + entra,
--    - sale (nunca cero). type:
--      purchase   -> entrada de compra (+), created_by = usuario.
--      adjustment -> ajuste/merma (+/-), reason obligatorio (capa service).
--      sale       -> descuento automático al vender (-), sale_id set, created_by
--                    NULL. Lo escribe una fase posterior.
CREATE TABLE IF NOT EXISTS supply_movements (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id     uuid NOT NULL REFERENCES tenants(id),
    supply_id     uuid NOT NULL REFERENCES supplies(id) ON DELETE CASCADE,
    branch_id     uuid NOT NULL REFERENCES branches(id),
    type          text NOT NULL CHECK (type IN ('purchase','adjustment','sale')),
    quantity_base integer NOT NULL CHECK (quantity_base <> 0), -- firmado, nunca 0
    reason        text,                                        -- requerido en adjustment (service)
    sale_id       uuid REFERENCES sales(id) ON DELETE SET NULL, -- solo type=sale
    created_by    uuid REFERENCES users(id),                    -- NULL en descuento automático
    created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS supply_movements_supply_created_idx ON supply_movements (tenant_id, supply_id, created_at);
CREATE INDEX IF NOT EXISTS supply_movements_sale_id_idx ON supply_movements (sale_id);
CREATE INDEX IF NOT EXISTS supply_movements_tenant_created_idx ON supply_movements (tenant_id, created_at);

-- Recomputación del cache desde el ledger (fuente de verdad). Úsese para reparar
-- drift; el ledger es autoritativo y el cache debe cumplir siempre la invariante
-- stock_base == SUM(quantity_base) por (supply_id, branch_id):
--
--   UPDATE supply_branch_stock s
--      SET stock_base = COALESCE((
--            SELECT SUM(m.quantity_base)
--              FROM supply_movements m
--             WHERE m.supply_id = s.supply_id
--               AND m.branch_id = s.branch_id
--          ), 0),
--          updated_at = now();

COMMIT;
