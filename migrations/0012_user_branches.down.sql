-- 0012_user_branches (down): restaura la asignación única users.branch_id y elimina
-- la tabla M:N. Colapsa las membresías a una sola sucursal por usuario (la menor por
-- id), por lo que se pierde la multi-sucursal (aceptable para un revert).

BEGIN;

ALTER TABLE users ADD COLUMN IF NOT EXISTS branch_id uuid REFERENCES branches(id);
CREATE INDEX IF NOT EXISTS users_branch_id_idx ON users (branch_id);

UPDATE users u
   SET branch_id = sub.branch_id
  FROM (
        SELECT user_id, MIN(branch_id::text)::uuid AS branch_id
          FROM user_branches
         GROUP BY user_id
       ) sub
 WHERE u.id = sub.user_id;

DROP TABLE IF EXISTS user_branches;

COMMIT;
