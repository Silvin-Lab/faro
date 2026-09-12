-- 0023_products_fulfillment_type (up): tipo de producto (M10 bakery, F1). Aditiva
-- sobre 0003 (products). No destructiva, sin backfill.
--
-- fulfillment_type distingue CÓMO se surte un producto:
--   'branch_prepared' (default): se prepara en la propia sucursal al venderse. La receta
--                    (product_supplies) se consume AL VENDER (comportamiento actual,
--                    sales.deductSupplies). NO tiene stock propio por sucursal.
--   'bakery':        surtido por la repostería central. La receta se consume AL PRODUCIR
--                    en la central (descuenta warehouse_stock), NO al vender. Tiene stock
--                    terminado propio por sucursal (product_branch_stock, 0025).
--
-- DOBLE SEMÁNTICA DE LA RECETA (ADR-010 D2, R8): product_supplies NO cambia de forma; se
-- REINTERPRETA según este campo. El MISMO insumo nunca se descuenta dos veces: un postre
-- 'bakery' descuenta al producir y su venta NO re-descuenta insumos (evita doble
-- contabilidad, R1). La disyunción se implementa con `AND p.fulfillment_type = '<tipo>'`
-- en el JOIN del punto de consumo de cada flujo (sales.deductSupplies /
-- sales.deductFinishedGoods / bakery.produce). Ver ADR-010 antes de tocar ese SQL.
--
-- Default 'branch_prepared' preserva el comportamiento de TODOS los productos vivos sin
-- backfill.

BEGIN;

ALTER TABLE products
    ADD COLUMN IF NOT EXISTS fulfillment_type text NOT NULL DEFAULT 'branch_prepared'
    CHECK (fulfillment_type IN ('branch_prepared','bakery'));

COMMIT;
