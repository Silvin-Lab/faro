-- M8: roles y permisos. Cada usuario tiene un rol explícito.
--   super_admin  -> acceso global (is_super_admin=true, tenant NULL, sin sucursales)
--   branch_admin -> POS + reportes forzados a su sucursal activa
--   cashier / barista -> solo POS (permisos idénticos; el rol es etiqueta)
ALTER TABLE users
    ADD COLUMN role text NOT NULL DEFAULT 'cashier'
    CHECK (role IN ('super_admin','branch_admin','cashier','barista'));

-- Backfill: los super admin existentes conservan su identidad; el resto queda 'cashier'.
UPDATE users SET role = 'super_admin' WHERE is_super_admin = true;
