-- 0028_supply_requisitions (down): revierte las requisiciones de insumos. Inverso del up,
-- en orden inverso (columna FK -> items -> header).

BEGIN;

ALTER TABLE warehouse_movements DROP COLUMN IF EXISTS requisition_id;

DROP TABLE IF EXISTS supply_requisition_items;
DROP TABLE IF EXISTS supply_requisitions;

COMMIT;
