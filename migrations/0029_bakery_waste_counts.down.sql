-- 0029_bakery_waste_counts (down): revierte el conteo de cierre de postres. Inverso del
-- up, en orden inverso (columna FK -> items -> header). No toca el CHECK de
-- product_stock_movements.type (no se modificó en el up).

BEGIN;

ALTER TABLE product_stock_movements DROP COLUMN IF EXISTS bakery_count_id;

DROP TABLE IF EXISTS bakery_count_items;
DROP TABLE IF EXISTS bakery_counts;

COMMIT;
