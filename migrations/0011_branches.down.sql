-- 0011_branches (down): revierte columnas y tabla de 0011. No hay pérdida de
-- datos preexistentes (todo lo eliminado es nuevo de 0011); se pierde la
-- atribución de sucursal de las ventas registradas mientras estuvo viva.

BEGIN;

DROP INDEX IF EXISTS sales_branch_id_idx;
ALTER TABLE sales DROP COLUMN IF EXISTS branch_id;

DROP INDEX IF EXISTS users_branch_id_idx;
ALTER TABLE users DROP COLUMN IF EXISTS branch_id;

ALTER TABLE tenants DROP COLUMN IF EXISTS favicon_url;

DROP INDEX IF EXISTS branches_tenant_id_idx;
DROP TABLE IF EXISTS branches;

COMMIT;
