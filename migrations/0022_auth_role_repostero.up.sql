-- 0022_auth_role_repostero (up): rol nuevo 'repostero' (M10 bakery, F20). Aditiva
-- sobre 0013 (users_role_check). No destructiva, sin backfill.
--
-- El repostero es tenant-scoped (a diferencia de super_admin, cuyo tenant_id es NULL):
-- lleva el tenant_id del negocio y CERO membresías en user_branches (F21: no pertenece
-- a ninguna sucursal ni se le puede asignar una). Su acceso está limitado al módulo de
-- producción (gating por rol inline en cada handler de /bakery y en /warehouse/stock).
--
-- El nombre de la constraint (verificado con \d users) es users_role_check.

BEGIN;

ALTER TABLE users DROP CONSTRAINT users_role_check;
ALTER TABLE users ADD CONSTRAINT users_role_check
    CHECK (role IN ('super_admin','branch_admin','cashier','barista','repostero'));

COMMIT;
