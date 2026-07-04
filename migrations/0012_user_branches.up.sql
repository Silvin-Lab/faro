-- 0012_user_branches (up): membresía M:N usuario<->sucursal (ADR-007). Reemplaza la
-- asignación única users.branch_id (0011). Conserva branches, sales.branch_id y
-- tenants.favicon_url. No destructiva salvo el drop de la columna migrada.

BEGIN;

CREATE TABLE IF NOT EXISTS user_branches (
    user_id    uuid NOT NULL REFERENCES users(id)    ON DELETE CASCADE,
    branch_id  uuid NOT NULL REFERENCES branches(id) ON DELETE CASCADE,
    tenant_id  uuid NOT NULL REFERENCES tenants(id), -- denormalizado (aislamiento)
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, branch_id)
);
CREATE INDEX IF NOT EXISTS user_branches_branch_id_idx ON user_branches (branch_id);
CREATE INDEX IF NOT EXISTS user_branches_tenant_id_idx ON user_branches (tenant_id);

-- Backfill desde la asignación única de 0011 (no-op si users.branch_id ya no existe
-- o no tiene datos).
INSERT INTO user_branches (user_id, branch_id, tenant_id)
SELECT id, branch_id, tenant_id FROM users WHERE branch_id IS NOT NULL
ON CONFLICT DO NOTHING;

-- Retirar la asignación única de 0011.
DROP INDEX IF EXISTS users_branch_id_idx;
ALTER TABLE users DROP COLUMN IF EXISTS branch_id;

COMMIT;
