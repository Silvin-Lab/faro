-- 0024_bakery_orders (down): revierte pedidos + producciones. Inverso del up.
-- Orden inverso: productions (referencia orders) antes que orders.

BEGIN;

DROP TABLE IF EXISTS bakery_productions;
DROP TABLE IF EXISTS bakery_orders;

COMMIT;
