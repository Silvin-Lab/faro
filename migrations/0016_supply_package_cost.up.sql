-- 0016_supply_package_cost (up): agrega el costo de UNA presentación al insumo.
-- Ej. "Bote 900 ml" cuesta $85.00 -> package_cost_cents = 8500. Aditiva: se aplica
-- ENCIMA de 0015 (ya viva). No destructiva.
--
-- NULLABLE a propósito: NULL = costo no capturado (desconocido). NO se usa 0 como
-- "desconocido" (0 sería un costo real de cero). CHECK (>= 0): si viene, no negativo.

BEGIN;

ALTER TABLE supplies ADD COLUMN IF NOT EXISTS package_cost_cents integer
    CHECK (package_cost_cents >= 0);

COMMIT;
