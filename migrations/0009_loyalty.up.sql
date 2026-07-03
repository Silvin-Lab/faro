-- 0009_loyalty (up): lealtad por visitas (módulo M6).

CREATE TABLE loyalty_configs (
    tenant_id        uuid PRIMARY KEY REFERENCES tenants(id),
    enabled          boolean NOT NULL DEFAULT true,
    discount_visits  integer,                    -- X (null = sin tier de descuento)
    discount_percent integer NOT NULL DEFAULT 0,
    free_visits      integer,                    -- Y (null = sin tier de gratis)
    updated_at       timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE loyalty_discount_products (
    tenant_id  uuid NOT NULL REFERENCES tenants(id),
    product_id uuid NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    PRIMARY KEY (tenant_id, product_id)
);

CREATE TABLE loyalty_free_products (
    tenant_id  uuid NOT NULL REFERENCES tenants(id),
    product_id uuid NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    PRIMARY KEY (tenant_id, product_id)
);

-- Contador de visitas del cliente (ciclo actual) — compartido entre sucursales.
ALTER TABLE customers ADD COLUMN visits integer NOT NULL DEFAULT 0;

-- Descuento de lealtad aplicado a la venta y tipo de recompensa.
ALTER TABLE sales ADD COLUMN discount_cents integer NOT NULL DEFAULT 0;
ALTER TABLE sales ADD COLUMN loyalty_reward text; -- null | 'discount' | 'free'
