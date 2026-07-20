-- 0018_supply_measures (up): medidas de uso por insumo. Un insumo comprado por
-- peso/volumen (unidad base g/ml/pieza) puede tener varias "medidas de uso" con
-- nombre + equivalencia en la unidad base (ej. scoop = 25 g, cucharada = 12 g). En
-- la receta, una línea puede capturarse por medida ("1 scoop") en vez de en la
-- unidad base. Aditiva: se aplica ENCIMA de 0017 (ya viva). No destructiva.
--
-- ESTRATEGIA (bajo riesgo): el descuento en venta (deductSupplies) sigue leyendo
-- product_supplies.quantity_base (integer, unidad base) — fuente de verdad, NO se
-- toca. El "en vivo" se logra recomputando quantity_base al guardar la receta y al
-- editar una medida (un UPDATE barato), no derivándolo en cada venta.

BEGIN;

-- 1. Medidas de uso de un insumo, tenant-scoped. base_quantity = equivalente de UNA
--    medida en la unidad base del insumo (ej. scoop = 25). ON DELETE CASCADE con el
--    insumo: borrar el insumo borra sus medidas (y product_supplies ya cae con el
--    insumo). UNIQUE(supply_id, name): no dos medidas con el mismo nombre por insumo.
CREATE TABLE IF NOT EXISTS supply_measures (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id     uuid NOT NULL REFERENCES tenants(id),
    supply_id     uuid NOT NULL REFERENCES supplies(id) ON DELETE CASCADE,
    name          text NOT NULL,
    base_quantity integer NOT NULL CHECK (base_quantity > 0), -- en unidad base
    created_at    timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT supply_measures_name_per_supply UNIQUE (supply_id, name)
);
CREATE INDEX IF NOT EXISTS supply_measures_supply_id_idx ON supply_measures (supply_id);

-- 2. La receta gana el "cómo se capturó" la línea (aditivo, opcional). quantity_base
--    (integer, unidad base) sigue siendo la fuente de verdad del descuento.
--    - Línea en unidad base: measure_id NULL, measure_count NULL, quantity_base = valor
--      capturado (comportamiento actual, sin cambios).
--    - Línea por medida: measure_id set, measure_count set, quantity_base = ROUND(
--      measure_count * supply_measures.base_quantity) (>= 1, valida el CHECK existente).
--    ON DELETE SET NULL: borrar una medida en uso deja la receta con su quantity_base
--    "congelado" en unidad base (sin etiqueta de medida). Documentado.
ALTER TABLE product_supplies
    ADD COLUMN IF NOT EXISTS measure_id uuid NULL REFERENCES supply_measures(id) ON DELETE SET NULL;
ALTER TABLE product_supplies
    ADD COLUMN IF NOT EXISTS measure_count numeric(10,3) NULL;

COMMIT;
