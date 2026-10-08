-- 0027_supply_branch_minmax (up): mín/máx por (insumo, sucursal) para el módulo de
-- requisiciones de insumos (M11). Aditiva. No destructiva.
--
-- Análogo a warehouse_stock.min_quantity/max_quantity (0019) pero a nivel de SUCURSAL:
-- permite que cada sucursal declare su umbral de reposición para sugerir automáticamente
-- qué pedir a la matriz (GET /requisitions/supplies/suggestions). Ambas columnas NULL =
-- sin umbral configurado (una sucursal solo aparece en las sugerencias si tiene min).

BEGIN;

ALTER TABLE supply_branch_stock
    ADD COLUMN min_quantity integer CHECK (min_quantity >= 0) NULL,
    ADD COLUMN max_quantity integer CHECK (max_quantity >= 0) NULL;

COMMIT;
