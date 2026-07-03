-- 0010_loyalty_promotions (down): revierte al esquema v1 (config única).
-- ATENCIÓN: DESTRUCTIVO. Se pierde el historial de canjes (loyalty_redemptions),
-- las promociones (loyalty_promotions/products) y el acumulado visits_lifetime.
-- Las tablas v1 se recrean VACÍAS; el detalle de loyalty_reward de ventas viejas
-- no se puede reconstruir. Aceptable: sistema reciente, datos de arranque.

BEGIN;

-- Eliminar tablas v2 (orden por dependencias).
DROP TABLE IF EXISTS loyalty_redemptions;
DROP TABLE IF EXISTS loyalty_promotion_products;
DROP TABLE IF EXISTS loyalty_promotions;

-- Revertir cambios sobre tablas existentes.
ALTER TABLE customers DROP COLUMN IF EXISTS visits_lifetime;
ALTER TABLE sales ADD COLUMN IF NOT EXISTS loyalty_reward text; -- null | 'discount' | 'free'

-- Recrear tablas v1 vacías.
CREATE TABLE IF NOT EXISTS loyalty_configs (
    tenant_id        uuid PRIMARY KEY REFERENCES tenants(id),
    enabled          boolean NOT NULL DEFAULT true,
    discount_visits  integer,
    discount_percent integer NOT NULL DEFAULT 0,
    free_visits      integer,
    updated_at       timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS loyalty_discount_products (
    tenant_id  uuid NOT NULL REFERENCES tenants(id),
    product_id uuid NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    PRIMARY KEY (tenant_id, product_id)
);

CREATE TABLE IF NOT EXISTS loyalty_free_products (
    tenant_id  uuid NOT NULL REFERENCES tenants(id),
    product_id uuid NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    PRIMARY KEY (tenant_id, product_id)
);

COMMIT;
