-- 0020_warehouse_adjustment (up): habilita el AJUSTE MANUAL de existencia del almacén.
-- Aditiva sobre 0019 (ya viva). No destructiva.
--
-- Contexto: hoy warehouse_stock.stock_base solo se mueve indirectamente vía
-- warehouse_movements de tipo purchase (+) / dispatch (−) / waste (−). Falta poder
-- FIJAR la existencia actual directamente (p.ej. alta de stock inicial de un insumo
-- sin simular una compra). Se agrega el tipo 'adjustment' al ledger del almacén: un
-- movimiento firmado con quantity_base = (nuevo total deseado − stock_base actual),
-- respetando el invariante warehouse_stock.stock_base == SUM(quantity_base). El
-- CHECK(quantity_base <> 0) sigue vigente: un ajuste a la MISMA cantidad no genera
-- movimiento (no-op en la capa service).

BEGIN;

ALTER TABLE warehouse_movements DROP CONSTRAINT warehouse_movements_type_check;
ALTER TABLE warehouse_movements ADD CONSTRAINT warehouse_movements_type_check
    CHECK (type IN ('purchase','dispatch','waste','adjustment'));

COMMIT;
