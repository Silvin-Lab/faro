-- 0024_bakery_orders (up): pedidos de sucursal a la repostería + producciones (M10,
-- F3-F14). Aditiva. No destructiva.
--
-- bakery_orders: un pedido de una sucursal a la repostería central (F3). Ciclo de estados
--   pending -> in_production -> shipped -> received, + cancelled (solo desde pending sin
--   producción). quantity_shipped acumula lo despachado por las producciones (F8/F9).
-- bakery_productions: cada acto de producción contra un pedido (F7), fuente de auditoría
--   (qué postre, cuánto, para qué sucursal, cuándo, quién). El doble efecto de stock
--   (descuento de insumos del almacén + acreditación de postre a la sucursal) se liga a la
--   producción vía FK (warehouse_movements.bakery_production_id, product_stock_movements
--   .bakery_production_id) en 0025/0026.

BEGIN;

-- Pedido de una sucursal a la repostería.
CREATE TABLE IF NOT EXISTS bakery_orders (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id        uuid NOT NULL REFERENCES tenants(id),
    -- branch_id SIN ON DELETE (igual que sales.branch_id): no perder trazabilidad; la
    -- protección de borrado de sucursal sigue vigente por la FK.
    branch_id        uuid NOT NULL REFERENCES branches(id),
    product_id       uuid NOT NULL REFERENCES products(id),
    quantity_ordered integer NOT NULL CHECK (quantity_ordered > 0),
    quantity_shipped integer NOT NULL DEFAULT 0 CHECK (quantity_shipped >= 0), -- acumulado (F8)
    status           text NOT NULL DEFAULT 'pending'
                     CHECK (status IN ('pending','in_production','shipped','received','cancelled')),
    note             text,                          -- opcional (F3)
    requested_by     uuid REFERENCES users(id),     -- quién creó el pedido
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS bakery_orders_status_idx  ON bakery_orders (tenant_id, status, created_at);
CREATE INDEX IF NOT EXISTS bakery_orders_branch_idx  ON bakery_orders (tenant_id, branch_id, created_at);
CREATE INDEX IF NOT EXISTS bakery_orders_product_idx ON bakery_orders (product_id);

-- Cada producción registrada contra un pedido (F7, auditoría).
CREATE TABLE IF NOT EXISTS bakery_productions (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id         uuid NOT NULL REFERENCES tenants(id),
    order_id          uuid NOT NULL REFERENCES bakery_orders(id) ON DELETE CASCADE,
    product_id        uuid NOT NULL REFERENCES products(id),   -- snapshot del postre producido
    branch_id         uuid NOT NULL REFERENCES branches(id),   -- snapshot de la sucursal destino
    quantity_produced integer NOT NULL CHECK (quantity_produced > 0),
    created_by        uuid REFERENCES users(id),               -- repostero/super_admin que registró
    created_at        timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS bakery_productions_order_idx  ON bakery_productions (tenant_id, order_id, created_at);
CREATE INDEX IF NOT EXISTS bakery_productions_audit_idx  ON bakery_productions (tenant_id, branch_id, product_id, created_at);

COMMIT;
