-- 0019_warehouse (up): módulo de almacén central (M8). Un almacén único por
-- negocio, intermedio entre "comprar" y "la sucursal consume". Aditiva: se aplica
-- ENCIMA de 0018 (ya viva). No destructiva. Ver ADR-008 y tech-spec §2.
--
-- Modelo (ADR-008 D1): DOS ledgers separados por dominio de stock. El almacén
-- lleva su propio stock (warehouse_stock) con su ledger firmado (warehouse_movements),
-- independiente del stock por sucursal (supply_branch_stock + supply_movements, 0015).
-- El catálogo NO se duplica: los ítems del almacén SON los supplies existentes.
-- Solo la salida (dispatch) cruza ambos dominios, en una transacción.
--
-- Orden: suppliers -> warehouse_stock -> warehouse_movements -> recién entonces
-- ALTER de supply_movements (su FK apunta a warehouse_movements). Igual que 0015-0018:
-- el stock del almacén PUEDE quedar negativo (sin CHECK de no-negatividad, R4).

BEGIN;

-- 1. Proveedores (catálogo tenant-scoped, F6). Baja SOFT vía status='inactive'
--    (consistente con supply_categories/supplies; preserva el historial de compras
--    que referencia al proveedor). UNIQUE(tenant_id, name): un nombre por negocio.
CREATE TABLE IF NOT EXISTS suppliers (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id  uuid NOT NULL REFERENCES tenants(id),
    name       text NOT NULL,
    address    text,
    email      text,
    phone      text,
    status     text NOT NULL DEFAULT 'active' CHECK (status IN ('active','inactive')),
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT suppliers_name_per_tenant UNIQUE (tenant_id, name)
);
CREATE INDEX IF NOT EXISTS suppliers_tenant_id_idx ON suppliers (tenant_id);

-- 2. Cache de existencias del almacén + mín/máx (F1-F4). Fila LAZY (como
--    supply_branch_stock): se crea al primer purchase o al primer guardado de
--    mín/máx. UNIQUE(supply_id): un almacén único (F1). stock_base PUEDE ser
--    negativo (SIN CHECK, R4). mín/máx en unidad base, opcionales; la validación
--    max >= min es de la capa service (para poder setear uno a la vez).
CREATE TABLE IF NOT EXISTS warehouse_stock (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id    uuid NOT NULL REFERENCES tenants(id),
    supply_id    uuid NOT NULL REFERENCES supplies(id) ON DELETE CASCADE,
    stock_base   integer NOT NULL DEFAULT 0, -- SIN CHECK: puede ser negativo (R4)
    min_quantity integer CHECK (min_quantity >= 0), -- unidad base; null = sin mínimo
    max_quantity integer CHECK (max_quantity >= 0), -- unidad base; null = sin máximo
    updated_at   timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT warehouse_stock_supply_unique UNIQUE (supply_id)
);
CREATE INDEX IF NOT EXISTS warehouse_stock_tenant_idx ON warehouse_stock (tenant_id);

-- 3. Ledger del almacén (fuente de verdad del stock central, F7/F12).
--    quantity_base FIRMADO: purchase (+), dispatch (-), waste (-); nunca 0.
--    Invariante: warehouse_stock.stock_base == SUM(warehouse_movements.quantity_base)
--    por supply_id. branch_id SOLO en dispatch (sucursal destino); purchase y waste
--    -> NULL (la merma CON sucursal NO vive aquí, vive en supply_movements; §3.3).
--    Campos de compra (supplier_id/packages/unit_cost_cents) SOLO en purchase:
--    unit_cost_cents = precio por presentación en centavos (F5), análogo puntual de
--    supplies.package_cost_cents (no muta el costo del catálogo).
CREATE TABLE IF NOT EXISTS warehouse_movements (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       uuid NOT NULL REFERENCES tenants(id),
    supply_id       uuid NOT NULL REFERENCES supplies(id) ON DELETE CASCADE,
    type            text NOT NULL CHECK (type IN ('purchase','dispatch','waste')),
    quantity_base   integer NOT NULL CHECK (quantity_base <> 0), -- firmado, nunca 0
    branch_id       uuid REFERENCES branches(id),                -- solo dispatch
    supplier_id     uuid REFERENCES suppliers(id) ON DELETE SET NULL, -- solo purchase
    packages        integer CHECK (packages > 0),         -- solo purchase: nº presentaciones
    unit_cost_cents integer CHECK (unit_cost_cents >= 0), -- solo purchase: precio/presentación
    reason          text,                                  -- requerido en waste (service)
    created_by      uuid REFERENCES users(id),             -- usuario que registró
    created_at      timestamptz NOT NULL DEFAULT now()     -- fecha del movimiento (§4.4)
);
CREATE INDEX IF NOT EXISTS warehouse_movements_supply_created_idx ON warehouse_movements (tenant_id, supply_id, created_at);
CREATE INDEX IF NOT EXISTS warehouse_movements_type_created_idx ON warehouse_movements (tenant_id, type, created_at);
CREATE INDEX IF NOT EXISTS warehouse_movements_branch_idx ON warehouse_movements (branch_id);
CREATE INDEX IF NOT EXISTS warehouse_movements_supplier_idx ON warehouse_movements (supplier_id);

-- 4. Extensión de supply_movements (pata de sucursal, ADR-008 D2/D3). Dos tipos
--    nuevos, ambos escritos por el módulo de almacén:
--      transfer (+): entrada a la sucursal por una SALIDA de almacén (dispatch).
--      waste (-):    merma de producto que YA estaba en la sucursal (§3.3 caso b);
--                    vive en el ledger de la sucursal porque los bienes están ahí.
ALTER TABLE supply_movements DROP CONSTRAINT supply_movements_type_check;
ALTER TABLE supply_movements ADD CONSTRAINT supply_movements_type_check
    CHECK (type IN ('purchase','adjustment','sale','transfer','waste'));

-- Trazabilidad (D3): liga la pata de sucursal (transfer) con su salida de almacén
-- origen. En waste de sucursal queda NULL (la merma no se ata a un dispatch puntual).
ALTER TABLE supply_movements
    ADD COLUMN IF NOT EXISTS warehouse_movement_id uuid NULL
    REFERENCES warehouse_movements(id) ON DELETE SET NULL;

COMMIT;
