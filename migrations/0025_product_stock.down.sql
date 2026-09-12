-- 0025_product_stock (down): revierte el stock de producto terminado. Inverso del up.
-- Orden inverso: product_stock_movements antes que product_branch_stock.

BEGIN;

DROP TABLE IF EXISTS product_stock_movements;
DROP TABLE IF EXISTS product_branch_stock;

COMMIT;
