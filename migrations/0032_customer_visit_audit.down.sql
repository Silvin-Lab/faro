-- 0032_customer_visit_audit (down): revierte la auditoría de visitas. Se pierde el
-- historial de cambios y la atribución del alta registrados mientras estuvo viva;
-- sin pérdida de datos preexistentes a la 0032.

BEGIN;

DROP INDEX IF EXISTS customer_visit_changes_tenant_idx;
DROP INDEX IF EXISTS customer_visit_changes_customer_idx;
DROP TABLE IF EXISTS customer_visit_changes;

ALTER TABLE customers DROP COLUMN IF EXISTS created_by;

COMMIT;
