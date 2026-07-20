-- 0018_supply_measures (down): revierte las medidas de uso. Primero se quitan las
-- columnas de product_supplies (dependen del FK a supply_measures) y luego la tabla.
-- Ambas son nuevas de 0018 (no hay pérdida de datos preexistentes; quantity_base se
-- conserva intacto).

BEGIN;

ALTER TABLE product_supplies DROP COLUMN IF EXISTS measure_id;
ALTER TABLE product_supplies DROP COLUMN IF EXISTS measure_count;
DROP TABLE IF EXISTS supply_measures;

COMMIT;
