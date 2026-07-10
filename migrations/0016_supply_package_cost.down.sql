-- 0016_supply_package_cost (down): revierte el costo de presentación. La columna es
-- nueva de 0016 (no hay pérdida de datos preexistentes).

BEGIN;

ALTER TABLE supplies DROP COLUMN IF EXISTS package_cost_cents;

COMMIT;
