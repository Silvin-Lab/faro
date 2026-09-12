-- 0025_product_stock (up): stock de producto terminado por sucursal (M10, F15/F16 ·
-- ADR-010 D1). Aditiva. No destructiva.
--
-- TERCER dominio de stock, análogo a los dos de ADR-008 (almacén y sucursal-insumo):
--   cache (product_branch_stock) + ledger firmado (product_stock_movements). Solo los
--   postres (products.fulfillment_type='bakery') tienen filas aquí; los 'branch_prepared'
--   nunca (su "stock" sigue implícito vía insumos).
--
-- Invariante: product_branch_stock.stock_qty == SUM(product_stock_movements.quantity) por
-- (product_id, branch_id). El cache se escribe SIEMPRE en la misma transacción que el
-- movimiento (producción / venta). Nada bloqueante: stock_qty PUEDE ser negativo (SIN
-- CHECK de no-negatividad, mismo criterio que warehouse_stock/supply_branch_stock, D-B).
--
-- Orden: product_branch_stock antes que product_stock_movements. La FK de
-- product_stock_movements.bakery_production_id apunta a bakery_productions (0024) => 0024
-- debe correr antes que 0025 (garantizado por el orden numérico del runner).

BEGIN;

-- Cache de existencias de postre por sucursal. Fila LAZY: se crea al primer production_in.
CREATE TABLE IF NOT EXISTS product_branch_stock (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id  uuid NOT NULL REFERENCES tenants(id),
    product_id uuid NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    branch_id  uuid NOT NULL REFERENCES branches(id),
    stock_qty  integer NOT NULL DEFAULT 0, -- SIN CHECK: puede ser negativo (D-B)
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT product_branch_stock_unique UNIQUE (product_id, branch_id)
);
CREATE INDEX IF NOT EXISTS product_branch_stock_branch_idx ON product_branch_stock (tenant_id, branch_id);

-- Ledger firmado del stock de postre (fuente de verdad).
--   quantity FIRMADO: production_in (+), sale (−), adjustment (±), waste (−); nunca 0.
--   NOTA MVP: solo se ESCRIBEN production_in (producción) y sale (venta). Los tipos
--   adjustment/waste se incluyen en el CHECK desde ahora (como 0015 incluyó 'sale' antes
--   de usarlo) para no re-migrar cuando se agregue el ajuste de stock de postre; NO hay
--   endpoint que los escriba en este MVP.
CREATE TABLE IF NOT EXISTS product_stock_movements (
    id                   uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id            uuid NOT NULL REFERENCES tenants(id),
    product_id           uuid NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    branch_id            uuid NOT NULL REFERENCES branches(id),
    type                 text NOT NULL CHECK (type IN ('production_in','sale','adjustment','waste')),
    quantity             integer NOT NULL CHECK (quantity <> 0), -- firmado, nunca 0
    bakery_production_id uuid REFERENCES bakery_productions(id) ON DELETE SET NULL, -- solo production_in
    sale_id              uuid REFERENCES sales(id) ON DELETE SET NULL,              -- solo sale
    reason               text,                          -- requerido en waste/adjustment (capa service; MVP no expone)
    created_by           uuid REFERENCES users(id),     -- NULL en descuento automático de venta
    created_at           timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS product_stock_movements_pbc_idx        ON product_stock_movements (tenant_id, product_id, branch_id, created_at);
CREATE INDEX IF NOT EXISTS product_stock_movements_sale_idx       ON product_stock_movements (sale_id);
CREATE INDEX IF NOT EXISTS product_stock_movements_production_idx ON product_stock_movements (bakery_production_id);

COMMIT;
