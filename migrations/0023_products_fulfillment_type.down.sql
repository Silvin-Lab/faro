-- 0023_products_fulfillment_type (down): revierte fulfillment_type. Inverso del up.

BEGIN;

ALTER TABLE products DROP COLUMN IF EXISTS fulfillment_type;

COMMIT;
