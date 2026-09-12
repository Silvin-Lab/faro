-- 0022_auth_role_repostero (down): revierte el rol 'repostero'. Inverso del up.
--
-- NOTA (como 0018/0019/0020): restaurar el CHECK sin 'repostero' FALLARÁ si ya existen
-- usuarios con ese rol. Es el comportamiento correcto: no perder datos. Para revertir en
-- ese caso hay que decidir explícitamente qué hacer con esos usuarios antes de bajar.

BEGIN;

ALTER TABLE users DROP CONSTRAINT users_role_check;
ALTER TABLE users ADD CONSTRAINT users_role_check
    CHECK (role IN ('super_admin','branch_admin','cashier','barista'));

COMMIT;
