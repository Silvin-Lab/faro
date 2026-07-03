-- 0009_loyalty (down)
ALTER TABLE sales DROP COLUMN IF EXISTS loyalty_reward;
ALTER TABLE sales DROP COLUMN IF EXISTS discount_cents;
ALTER TABLE customers DROP COLUMN IF EXISTS visits;
DROP TABLE IF EXISTS loyalty_free_products;
DROP TABLE IF EXISTS loyalty_discount_products;
DROP TABLE IF EXISTS loyalty_configs;
