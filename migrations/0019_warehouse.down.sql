-- 0019_warehouse (down): revierte el módulo de almacén. Inverso del up: primero se
-- revierte la extensión de supply_movements (columna FK + CHECK), luego se dropean
-- las 3 tablas nuevas (warehouse_movements antes por su FK a suppliers).
--
-- NOTA (como 0018): restaurar el CHECK sin 'transfer'/'waste' FALLARÁ si ya existen
-- filas de esos tipos en supply_movements (movimientos de salida/merma ya
-- registrados). Es el comportamiento correcto: no perder datos. Para revertir en ese
-- caso hay que decidir explícitamente qué hacer con esos movimientos antes de bajar.

BEGIN;

-- Quitar la columna FK primero (depende de warehouse_movements).
ALTER TABLE supply_movements DROP COLUMN IF EXISTS warehouse_movement_id;

-- Restaurar el CHECK original (sin transfer/waste). Falla si hay filas transfer/waste.
ALTER TABLE supply_movements DROP CONSTRAINT supply_movements_type_check;
ALTER TABLE supply_movements ADD CONSTRAINT supply_movements_type_check
    CHECK (type IN ('purchase','adjustment','sale'));

DROP TABLE IF EXISTS warehouse_movements;
DROP TABLE IF EXISTS warehouse_stock;
DROP TABLE IF EXISTS suppliers;

COMMIT;
