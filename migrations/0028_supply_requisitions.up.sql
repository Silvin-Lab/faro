-- 0028_supply_requisitions (up): requisiciones de insumos de sucursal a la matriz (M11).
-- Aditiva. No destructiva.
--
-- Patrón "sucursal solicita -> cola visible para quien resuelve -> dispatch con doble
-- efecto y trazabilidad", réplica de bakery_orders/bakery_productions pero sobre insumos.
-- El surtido se hace vía warehouse dispatch (0019): warehouse_movements.requisition_id
-- liga cada salida a la requisición que la origina, para mostrar la traza de qué se surtió.
--
-- Cantidades en UNIDAD BASE (mismo criterio que todo el módulo de almacén/insumos).

BEGIN;

CREATE TABLE supply_requisitions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL REFERENCES tenants(id),
    branch_id uuid NOT NULL REFERENCES branches(id),
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','partial','fulfilled','cancelled')),
    note text,
    requested_by uuid REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX supply_requisitions_status_idx ON supply_requisitions (tenant_id, status, created_at);
CREATE INDEX supply_requisitions_branch_idx ON supply_requisitions (tenant_id, branch_id, created_at);

CREATE TABLE supply_requisition_items (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL REFERENCES tenants(id),
    requisition_id uuid NOT NULL REFERENCES supply_requisitions(id) ON DELETE CASCADE,
    supply_id uuid NOT NULL REFERENCES supplies(id),
    quantity_requested integer NOT NULL CHECK (quantity_requested > 0),
    quantity_fulfilled integer NOT NULL DEFAULT 0 CHECK (quantity_fulfilled >= 0),
    note text,
    CONSTRAINT supply_requisition_items_unique UNIQUE (requisition_id, supply_id)
);
CREATE INDEX supply_requisition_items_req_idx ON supply_requisition_items (requisition_id);

ALTER TABLE warehouse_movements ADD COLUMN requisition_id uuid NULL REFERENCES supply_requisitions(id) ON DELETE SET NULL;

COMMIT;
