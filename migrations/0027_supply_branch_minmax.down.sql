-- 0027_supply_branch_minmax (down): elimina el mín/máx por sucursal. Inverso del up.

BEGIN;

ALTER TABLE supply_branch_stock DROP COLUMN IF EXISTS max_quantity;
ALTER TABLE supply_branch_stock DROP COLUMN IF EXISTS min_quantity;

COMMIT;
