-- 0031_agreement_discounts (up): descuentos de convenio en el POS (módulo M12).
-- Ver ADR-010 y .arete/modules/convenio-discounts/tech-spec.md.
--
-- Aditiva: se aplica ENCIMA de 0020 (0021-0030 están reservadas por M10/M11 en
-- otras ramas y NO existen todavía; la 0031 es autocontenida y no depende de ellas).
-- No destructiva. Patrón de catálogo tomado de loyalty_promotions (ADR-005).

BEGIN;

-- 1. Catálogo de descuentos de convenio (un % sobre toda la compra). Soft-delete
--    vía status='inactive' (archivar preserva la trazabilidad de ventas pasadas).
CREATE TABLE IF NOT EXISTS agreement_discounts (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id  uuid NOT NULL REFERENCES tenants(id),
    percent    integer NOT NULL CHECK (percent BETWEEN 1 AND 100),
    status     text NOT NULL DEFAULT 'active', -- active | inactive (archivada)
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- % único por tenant SOLO entre los activos (permite archivar y recrear el mismo %).
CREATE UNIQUE INDEX IF NOT EXISTS agreement_discounts_tenant_percent_active_uq
    ON agreement_discounts (tenant_id, percent) WHERE status = 'active';

-- Lectura desde el POS (listar activos) y admin (filtro por ?status).
CREATE INDEX IF NOT EXISTS agreement_discounts_tenant_status_idx
    ON agreement_discounts (tenant_id, status);

-- 2. Columnas nuevas en sales (ADR-010 §D1, §D4). Aditivas y nullables donde aplica.
--    discount_cents NO cambia de significado: sigue siendo SOLO el descuento de
--    lealtad. total_cents sigue neto (ahora de ambos descuentos).
ALTER TABLE sales ADD COLUMN IF NOT EXISTS agreement_discount_id      uuid    REFERENCES agreement_discounts(id) ON DELETE SET NULL;
ALTER TABLE sales ADD COLUMN IF NOT EXISTS agreement_discount_percent integer CHECK (agreement_discount_percent BETWEEN 1 AND 100);
ALTER TABLE sales ADD COLUMN IF NOT EXISTS agreement_discount_cents   integer NOT NULL DEFAULT 0;
ALTER TABLE sales ADD COLUMN IF NOT EXISTS user_id                    uuid    REFERENCES users(id);

CREATE INDEX IF NOT EXISTS sales_user_id_idx ON sales (user_id);

-- 3. Seed 10% y 15% por tenant. Idempotente si se re-corre (choca con el índice
--    parcial de activos -> ON CONFLICT DO NOTHING).
INSERT INTO agreement_discounts (tenant_id, percent)
SELECT t.id, v.percent
  FROM tenants t CROSS JOIN (VALUES (10), (15)) AS v(percent)
ON CONFLICT DO NOTHING;

COMMIT;
