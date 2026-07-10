-- 0015_supplies (down): revierte el módulo de insumos. Todo lo eliminado es nuevo
-- de 0015 (no hay pérdida de datos preexistentes). Orden inverso al up.

BEGIN;

DROP INDEX IF EXISTS supply_movements_tenant_created_idx;
DROP INDEX IF EXISTS supply_movements_sale_id_idx;
DROP INDEX IF EXISTS supply_movements_supply_created_idx;
DROP TABLE IF EXISTS supply_movements;

DROP INDEX IF EXISTS supply_branch_stock_tenant_branch_idx;
DROP TABLE IF EXISTS supply_branch_stock;

DROP INDEX IF EXISTS product_supplies_supply_id_idx;
DROP INDEX IF EXISTS product_supplies_product_id_idx;
DROP TABLE IF EXISTS product_supplies;

DROP INDEX IF EXISTS supplies_tenant_id_idx;
DROP TABLE IF EXISTS supplies;

COMMIT;
