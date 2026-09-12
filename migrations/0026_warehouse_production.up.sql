-- 0026_warehouse_production (up): insumo consumido al producir en la repostería (M10 ·
-- ADR-010 D3). Aditiva sobre 0019/0020. No destructiva.
--
-- DESVIACIÓN DEL PLAN: el CHECK ya incluye 'adjustment' (migración 0020). Se EXTIENDE a
-- 5 tipos (no se reemplazan 3): + 'production'.
--   type='production': quantity_base NEGATIVO, branch_id NULL (el insumo se consume
--   CENTRALMENTE, no se despacha a una sucursal — así se distingue de 'dispatch'), y
--   bakery_production_id set (trazabilidad del consumo -> la producción que lo causó,
--   mismo patrón que supply_movements.warehouse_movement_id).
--
-- Orden: la FK bakery_production_id apunta a bakery_productions (0024) => 0024 antes que
-- 0026 (garantizado por el orden numérico del runner).

BEGIN;

ALTER TABLE warehouse_movements DROP CONSTRAINT warehouse_movements_type_check;
ALTER TABLE warehouse_movements ADD CONSTRAINT warehouse_movements_type_check
    CHECK (type IN ('purchase','dispatch','waste','adjustment','production'));

ALTER TABLE warehouse_movements
    ADD COLUMN IF NOT EXISTS bakery_production_id uuid NULL
    REFERENCES bakery_productions(id) ON DELETE SET NULL;

COMMIT;
