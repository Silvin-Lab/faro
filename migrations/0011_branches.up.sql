-- 0011_branches (up): sucursales por negocio, sucursal por usuario/venta y favicon
-- por tenant. Se aplica ENCIMA de 0010 (ya viva). No destructiva. Ver ADR-006.

BEGIN;

-- 1. Tabla nueva branches (tenant-scoped, nombre único por negocio) -----------

CREATE TABLE IF NOT EXISTS branches (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id  uuid NOT NULL REFERENCES tenants(id),
    name       text NOT NULL,                  -- editable (ej. "Vanta Centro")
    status     text NOT NULL DEFAULT 'active', -- active | inactive (archivada)
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT branches_name_per_tenant UNIQUE (tenant_id, name)
);
CREATE INDEX IF NOT EXISTS branches_tenant_id_idx ON branches (tenant_id);

-- 2. users.branch_id (sucursal asignada al usuario, nullable) -----------------

ALTER TABLE users ADD COLUMN IF NOT EXISTS branch_id uuid REFERENCES branches(id);
CREATE INDEX IF NOT EXISTS users_branch_id_idx ON users (branch_id);

-- 3. sales.branch_id (sucursal de la venta, derivada del usuario, nullable) ----

ALTER TABLE sales ADD COLUMN IF NOT EXISTS branch_id uuid REFERENCES branches(id);
CREATE INDEX IF NOT EXISTS sales_branch_id_idx ON sales (branch_id);

-- 4. tenants.favicon_url (URL relativa de /files/*, nullable) -----------------

ALTER TABLE tenants ADD COLUMN IF NOT EXISTS favicon_url text;

COMMIT;
