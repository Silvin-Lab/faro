# Preguntas abiertas y decisiones — M7 v2 business-settings

_Fecha: 2026-07-03 · Decisiones: ADR-006 + ADR-007_

## P1 — CERRADO: **MVP-A (sin roles)** elegido por el usuario

- **Roles:** no se introducen. Dos perfiles: **super admin** y **usuario de sucursal**.
- **products/categories/reports/loyalty:** **Opción A**. El usuario de sucursal solo ve **POS**
  (lee catálogo/lealtad/clientes por API); escritura de catálogo, CRUD de loyalty y **reportes**
  son **solo super admin**. `GET /sales` del usuario se acota a su **sucursal activa**; no ve
  otras sucursales.
- Matriz de acceso por endpoint y menús por perfil: **`api-contract.md` §7-8** (fuente de verdad).

## P2 — Afinamientos con default (no bloquean)

- **Usuario sin sucursal activa** (operativo que no eligió, o 0 membresías): `POST /sales`
  → `400 branch_required`. ¿Se exige ≥1 membresía en el alta (default sí) y se bloquea el
  POS hasta elegir?
- **Super admin y POS:** el super admin no tiene sucursal → no opera POS/ventas. ¿Correcto,
  o el super admin debe poder operar eligiendo una sucursal puntualmente? (Default: no opera.)
- **Seed/datos:** con "negocio único", ¿se re-siembra un único `tenants` + super admin y los
  antiguos "owner" tenant-scoped pasan a ser personal de sucursal? Confirmar plan de datos.
- **Cambiar de sucursal:** vía `POST /auth/select-branch` (re-emite JWT). ¿Se expone siempre
  en el header o solo cuando `branches.length > 1`? (Default: solo si >1.)

## Decisiones cerradas (ya en ADR-006/007 y docs)
- Negocio único; super admin dueño global; `businessTenantID()`; `POST /tenants` retirado.
- M:N `user_branches` (0012), retira `users.branch_id`.
- Sucursal activa por claim JWT + `/auth/select-branch`; `sales.branch_id` derivado en servidor.
- Encabezado `Faro. {sucursal activa}`. Favicon por tenant (uploads + runtime).
- Administración (branches/favicon/users) **solo super admin**.

## Extensiones futuras
- Roles por sucursal; FK compuesta `(tenant_id, branch_id)`; atributos de sucursal;
  migrar `favicon_url` a `tenant_settings`.
