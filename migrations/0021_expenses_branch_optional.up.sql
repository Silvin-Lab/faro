-- 0021_expenses_branch_optional (up): permite gastos SIN sucursal (bucket "General").
-- Aditiva sobre 0014 (ya viva). No destructiva: los gastos existentes conservan su
-- branch_id; a partir de ahora branch_id puede ser NULL para gastos corporativos que
-- registra la administración central (super_admin) y que no pertenecen a ninguna
-- sucursal (software, contador, legal, etc.).
--
-- Deliberadamente NO se agrega ON DELETE SET NULL al FK branch_id: es el mismo
-- criterio que sales.branch_id. Sin ON DELETE, borrar una sucursal con historial de
-- gastos falla por violación de FK (la protección de internal/branches/store.go
-- delete sigue vigente), preservando la trazabilidad del gasto por sucursal.

BEGIN;

ALTER TABLE expenses ALTER COLUMN branch_id DROP NOT NULL;

COMMIT;
