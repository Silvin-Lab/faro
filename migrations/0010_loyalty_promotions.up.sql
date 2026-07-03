-- 0010_loyalty_promotions (up): lealtad v2 — de config única a CRUD de promociones.
-- Se aplica ENCIMA de 0009 (ya viva con datos): crea lo nuevo, transforma la
-- config v1 en promociones y elimina las tablas v1. Ver ADR-005.

BEGIN;

-- 1. Tablas nuevas -----------------------------------------------------------

CREATE TABLE IF NOT EXISTS loyalty_promotions (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id        uuid NOT NULL REFERENCES tenants(id),
    name             text NOT NULL,
    discount_percent integer NOT NULL CHECK (discount_percent BETWEEN 1 AND 100), -- 100 = gratis
    visit_threshold  integer NOT NULL CHECK (visit_threshold > 0),
    resets_counter   boolean NOT NULL DEFAULT false,
    status           text NOT NULL DEFAULT 'active', -- active | inactive (archivada)
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS loyalty_promotions_tenant_status_idx
    ON loyalty_promotions (tenant_id, status);

CREATE TABLE IF NOT EXISTS loyalty_promotion_products (
    promotion_id uuid NOT NULL REFERENCES loyalty_promotions(id) ON DELETE CASCADE,
    product_id   uuid NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    tenant_id    uuid NOT NULL REFERENCES tenants(id), -- denormalizado para aislamiento
    PRIMARY KEY (promotion_id, product_id)
);
CREATE INDEX IF NOT EXISTS loyalty_promotion_products_product_idx
    ON loyalty_promotion_products (product_id);

CREATE TABLE IF NOT EXISTS loyalty_redemptions (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id          uuid NOT NULL REFERENCES tenants(id),
    customer_id        uuid NOT NULL REFERENCES customers(id),
    sale_id            uuid NOT NULL REFERENCES sales(id),
    promotion_id       uuid REFERENCES loyalty_promotions(id) ON DELETE SET NULL,
    -- snapshot de la promoción al momento del canje:
    promotion_name     text    NOT NULL,
    discount_percent   integer NOT NULL,
    visit_threshold    integer NOT NULL,
    caused_reset       boolean NOT NULL,
    -- estado del cliente en el instante del canje:
    visits_cycle_at    integer NOT NULL, -- contador de ciclo alcanzado (post-incremento)
    visits_lifetime_at integer NOT NULL, -- acumulado de por vida en ese instante
    discount_cents     integer NOT NULL,
    created_at         timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS loyalty_redemptions_tenant_customer_idx
    ON loyalty_redemptions (tenant_id, customer_id, created_at);

-- 2. customers.visits_lifetime (acumulado; backfill piso = visits actual) -----

ALTER TABLE customers ADD COLUMN IF NOT EXISTS visits_lifetime integer NOT NULL DEFAULT 0;
UPDATE customers SET visits_lifetime = visits WHERE visits_lifetime = 0 AND visits > 0;

-- 3. Transformar la config v1 en promociones ---------------------------------

-- (a) Tier de descuento -> promoción sin reinicio.
WITH ins AS (
    INSERT INTO loyalty_promotions (tenant_id, name, discount_percent, visit_threshold, resets_counter, status)
    SELECT c.tenant_id, 'Descuento', c.discount_percent, c.discount_visits, false, 'active'
      FROM loyalty_configs c
     WHERE c.discount_visits IS NOT NULL
       AND c.discount_visits > 0
       AND c.discount_percent BETWEEN 1 AND 100
       AND EXISTS (SELECT 1 FROM loyalty_discount_products dp WHERE dp.tenant_id = c.tenant_id)
    RETURNING id, tenant_id
)
INSERT INTO loyalty_promotion_products (promotion_id, product_id, tenant_id)
SELECT ins.id, dp.product_id, ins.tenant_id
  FROM ins
  JOIN loyalty_discount_products dp ON dp.tenant_id = ins.tenant_id;

-- (b) Tier gratis -> promoción 100% con reinicio.
WITH ins AS (
    INSERT INTO loyalty_promotions (tenant_id, name, discount_percent, visit_threshold, resets_counter, status)
    SELECT c.tenant_id, 'Producto gratis', 100, c.free_visits, true, 'active'
      FROM loyalty_configs c
     WHERE c.free_visits IS NOT NULL
       AND c.free_visits > 0
       AND EXISTS (SELECT 1 FROM loyalty_free_products fp WHERE fp.tenant_id = c.tenant_id)
    RETURNING id, tenant_id
)
INSERT INTO loyalty_promotion_products (promotion_id, product_id, tenant_id)
SELECT ins.id, fp.product_id, ins.tenant_id
  FROM ins
  JOIN loyalty_free_products fp ON fp.tenant_id = ins.tenant_id;

-- 4. sales: se elimina loyalty_reward (reemplazado por loyalty_redemptions) ----

ALTER TABLE sales DROP COLUMN IF EXISTS loyalty_reward;

-- 5. Eliminar tablas v1 ------------------------------------------------------

DROP TABLE IF EXISTS loyalty_discount_products;
DROP TABLE IF EXISTS loyalty_free_products;
DROP TABLE IF EXISTS loyalty_configs;

COMMIT;
