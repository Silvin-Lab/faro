-- 0029_bakery_waste_counts (up): conteo de cierre de postres por sucursal con
-- reconciliación automática (merma/ajuste) contra el stock de postre (M11). Aditiva.
-- No destructiva.
--
-- bakery_counts: un conteo físico de postres al cierre del día en una sucursal.
-- bakery_count_items: por producto, lo esperado (stock en cache al momento del conteo),
--   lo contado y la diferencia; movement_id liga al movimiento de merma/ajuste que la
--   reconciliación generó (NULL si diff=0, sin movimiento por el CHECK quantity<>0).
--
-- product_stock_movements.type YA acepta 'adjustment'/'waste' desde 0025: NO se toca ese
-- CHECK. Solo se agrega la columna de trazabilidad bakery_count_id (análoga a
-- bakery_production_id).
--
-- Orden: la FK bakery_count_id apunta a bakery_counts => se crea la tabla antes de la
-- columna (garantizado dentro de esta misma migración).

BEGIN;

CREATE TABLE bakery_counts (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL REFERENCES tenants(id),
    branch_id uuid NOT NULL REFERENCES branches(id),
    note text,
    created_by uuid REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX bakery_counts_branch_idx ON bakery_counts (tenant_id, branch_id, created_at);

CREATE TABLE bakery_count_items (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL REFERENCES tenants(id),
    count_id uuid NOT NULL REFERENCES bakery_counts(id) ON DELETE CASCADE,
    product_id uuid NOT NULL REFERENCES products(id),
    expected_qty integer NOT NULL,
    counted_qty integer NOT NULL CHECK (counted_qty >= 0),
    diff_qty integer NOT NULL,
    movement_id uuid REFERENCES product_stock_movements(id) ON DELETE SET NULL,
    CONSTRAINT bakery_count_items_unique UNIQUE (count_id, product_id)
);
CREATE INDEX bakery_count_items_count_idx ON bakery_count_items (count_id);

ALTER TABLE product_stock_movements ADD COLUMN bakery_count_id uuid NULL REFERENCES bakery_counts(id) ON DELETE SET NULL;

COMMIT;
