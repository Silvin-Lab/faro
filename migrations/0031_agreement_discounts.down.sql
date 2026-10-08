-- 0031_agreement_discounts (down): revierte el módulo M12. Se pierde la atribución
-- de convenio/usuario de las ventas registradas mientras la migración estuvo viva
-- (mismo criterio que 0011); no hay pérdida de datos preexistentes a la 0031.

BEGIN;

DROP INDEX IF EXISTS sales_user_id_idx;
ALTER TABLE sales DROP COLUMN IF EXISTS user_id;
ALTER TABLE sales DROP COLUMN IF EXISTS agreement_discount_cents;
ALTER TABLE sales DROP COLUMN IF EXISTS agreement_discount_percent;
ALTER TABLE sales DROP COLUMN IF EXISTS agreement_discount_id;

DROP INDEX IF EXISTS agreement_discounts_tenant_status_idx;
DROP INDEX IF EXISTS agreement_discounts_tenant_percent_active_uq;
DROP TABLE IF EXISTS agreement_discounts;

COMMIT;
