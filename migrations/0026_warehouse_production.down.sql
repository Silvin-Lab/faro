-- 0026_warehouse_production (down): revierte el consumo de producción. Inverso del up.
-- Restaura el CHECK a los 4 tipos previos (con 'adjustment', NO 3).
--
-- NOTA (como 0020): restaurar el CHECK sin 'production' FALLARÁ si ya existen filas de ese
-- tipo. Es el comportamiento correcto: no perder datos. Decidir explícitamente qué hacer
-- con esos movimientos antes de bajar.

BEGIN;

ALTER TABLE warehouse_movements DROP COLUMN IF EXISTS bakery_production_id;

ALTER TABLE warehouse_movements DROP CONSTRAINT warehouse_movements_type_check;
ALTER TABLE warehouse_movements ADD CONSTRAINT warehouse_movements_type_check
    CHECK (type IN ('purchase','dispatch','waste','adjustment'));

COMMIT;
