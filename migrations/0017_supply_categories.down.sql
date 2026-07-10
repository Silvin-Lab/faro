-- 0017_supply_categories (down): revierte las categorías de insumo. Primero se quita
-- la columna category_id de supplies (dependencia del FK) y luego la tabla. Ambas son
-- nuevas de 0017 (no hay pérdida de datos preexistentes).

BEGIN;

ALTER TABLE supplies DROP COLUMN IF EXISTS category_id;
DROP TABLE IF EXISTS supply_categories;

COMMIT;
