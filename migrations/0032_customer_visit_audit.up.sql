-- 0032_customer_visit_audit (up): auditoría de visitas de clientes.
--
-- Contexto: desde 0008/0021 el alta de cliente acepta priorVisits y el PATCH de
-- visitas existe; no se registraba quién creó al cliente ni quién ajustó/sembró
-- visitas. Esta migración agrega la atribución del alta y el historial de cambios
-- de visitas (origen 'create' con priorVisits>0, u 'adjust' en el PATCH).
--
-- Aditiva y no destructiva: se aplica ENCIMA de 0031. created_by y user_id son
-- nullables (filas históricas quedan NULL).

BEGIN;

-- Quién registró al cliente (de la sesión; históricos NULL).
ALTER TABLE customers ADD COLUMN IF NOT EXISTS created_by uuid REFERENCES users(id);

-- Historial de cambios de visitas (append-only). Un registro por evento que fija
-- visitas: alta con priorVisits>0 ('create') o ajuste manual ('adjust').
CREATE TABLE IF NOT EXISTS customer_visit_changes (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id     uuid NOT NULL REFERENCES tenants(id),
    customer_id   uuid NOT NULL REFERENCES customers(id) ON DELETE CASCADE,
    user_id       uuid REFERENCES users(id), -- de la sesión; NULL si no hubo (robustez)
    visits_before integer NOT NULL,
    visits_after  integer NOT NULL,
    source        text NOT NULL CHECK (source IN ('create', 'adjust')),
    created_at    timestamptz NOT NULL DEFAULT now()
);

-- Historial de un cliente, más recientes primero.
CREATE INDEX IF NOT EXISTS customer_visit_changes_customer_idx
    ON customer_visit_changes (customer_id, created_at DESC);
CREATE INDEX IF NOT EXISTS customer_visit_changes_tenant_idx
    ON customer_visit_changes (tenant_id);

COMMIT;
