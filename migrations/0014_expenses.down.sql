-- 0014_expenses (down): revierte el módulo de gastos. Todo lo eliminado es nuevo
-- de 0014 (no hay pérdida de datos preexistentes). Orden inverso al up.

BEGIN;

DROP INDEX IF EXISTS expenses_tenant_created_idx;
DROP INDEX IF EXISTS expenses_tenant_branch_created_idx;
DROP TABLE IF EXISTS expenses;

DROP INDEX IF EXISTS expense_concepts_tenant_id_idx;
DROP TABLE IF EXISTS expense_concepts;

DROP INDEX IF EXISTS expense_categories_tenant_id_idx;
DROP TABLE IF EXISTS expense_categories;

COMMIT;
