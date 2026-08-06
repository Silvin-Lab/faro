-- 0020_warehouse_adjustment (down): revierte el tipo 'adjustment' del ledger del
-- almacén. Inverso del up.
--
-- NOTA (como 0018/0019): restaurar el CHECK sin 'adjustment' FALLARÁ si ya existen
-- filas de ese tipo en warehouse_movements (ajustes ya registrados). Es el
-- comportamiento correcto: no perder datos. Para revertir en ese caso hay que decidir
-- explícitamente qué hacer con esos movimientos antes de bajar.

BEGIN;

ALTER TABLE warehouse_movements DROP CONSTRAINT warehouse_movements_type_check;
ALTER TABLE warehouse_movements ADD CONSTRAINT warehouse_movements_type_check
    CHECK (type IN ('purchase','dispatch','waste'));

COMMIT;
