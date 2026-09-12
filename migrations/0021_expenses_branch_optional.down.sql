-- 0021_expenses_branch_optional (down): restaura branch_id NOT NULL en expenses.
-- Inverso del up.
--
-- NOTA: restaurar el NOT NULL FALLARÁ si ya existen gastos "General" (branch_id NULL)
-- registrados por la administración central. Es el comportamiento correcto: no perder
-- datos. Para revertir en ese caso hay que decidir explícitamente qué hacer con esos
-- gastos (reasignarlos a una sucursal o borrarlos) antes de bajar.

BEGIN;

ALTER TABLE expenses ALTER COLUMN branch_id SET NOT NULL;

COMMIT;
