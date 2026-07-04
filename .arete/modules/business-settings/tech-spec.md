# Tech Spec — M7 v2 business-settings (negocio único, sucursales + favicon)

_Fecha: 2026-07-03 · Módulo: M7 · Decisiones: ADR-006 + **ADR-007** · **Estado: diseño listo — decisiones cerradas (MVP-A sin roles)**_

Docs hermanos: `data-model.md` (0011 + 0012), `api-contract.md`, `frontend-flows.md`,
`open-questions.md`. Decisiones: `ADR-006`, `ADR-007`.

## 1. Objetivo
Un solo negocio por instancia. El **super admin** (dueño global) administra **sucursales**,
**favicon** y **usuarios** (asignándolos a **1+ sucursales**, M:N). El personal, tras login,
selecciona su **sucursal activa** (si tiene >1); esa sucursal etiqueta cada venta
(`sales.branch_id`) y el POS muestra `Faro. {sucursal activa}`.

## 2. Arquitectura y límites
- **Negocio único (ADR-007 §D1-D2):** una fila en `tenants`; se conserva el scoping por
  tenant. El super admin sigue global y resuelve el negocio con `businessTenantID()`. Se
  **retira** `POST /tenants`.
- **Autorización:** nuevo middleware `RequireSuperAdmin` para `branches`, `settings`,
  `users`. Resto con `RequireSession`.
- **Sucursal activa (ADR-007 §D4-D5):** claim `activeBranchId` en el JWT; `POST
  /auth/select-branch` lo fija re-emitiendo la cookie; `POST /sales` lo lee (server-trusted).
- **Backend:** módulos nuevos `internal/branches`, `internal/settings`; middleware
  `RequireSuperAdmin`; claim + `select-branch` + `/auth/me`/login extendidos en `auth`;
  `/users` con M:N; `sales` deriva branch del claim; `reports` con filtro/desglose.
- **Frontend:** guard de selección post-login, `/select-branch`, admin super-admin-only,
  encabezado POS, `FaviconManager`.
- **Reutiliza:** `POST /uploads` + `/files/*` (favicon).

## 3. Modelo de datos (ver data-model.md)
- 0011: `branches`, `sales.branch_id`, `tenants.favicon_url` (se conservan).
  `users.branch_id` **obsoleto** (omitir si no se implementó).
- 0012: `user_branches(user_id, branch_id, tenant_id)` M:N; retira `users.branch_id`.

## 4. Contrato (ver api-contract.md)
`/branches`, `/settings/favicon`, `/users` (branchIds) → **super admin**.
`/auth/login` y `/auth/me` → `branches`, `activeBranchId`, `mustSelectBranch`, `tenant`.
`/auth/select-branch` → fija sucursal activa (re-emite JWT). `POST /sales` deriva del claim.
`/reports?branchId=` + `byBranch`.

## 5. No funcionales
- **Aislamiento:** todo acotado al tenant del negocio (`businessTenantID()` / `tenantOf`);
  membresía y sucursal activa validadas en servidor.
- **Seguridad:** sucursal activa en JWT (no confiar en el cliente); admin tras
  `RequireSuperAdmin`; favicon solo `/files/*`.
- **Rendimiento:** índices por `tenant_id`, `user_branches(branch_id)`, `sales.branch_id`.

## 6. Riesgos y deuda
- **Roles:** **cerrado en MVP-A (sin roles).** Dos perfiles: super admin (todo el admin +
  reportes) y usuario de sucursal (solo POS). Matriz de acceso y menús en `api-contract.md`
  §7-8. Un futuro owner/barista por sucursal requeriría un sistema de roles (fuera de MVP).
- **`resolveTenant`:** el super admin (`tenant_id NULL`) resuelve `businessTenantID()` en
  products/categories/loyalty(write)/reports/uploads; deuda menor de refactor del actual
  `targetTenant`/`tenantOf`.
- **Owner previo:** el owner tenant-scoped desaparece; sus usuarios se re-siembran como
  personal de sucursal (seed/migración de datos a confirmar).
- **Re-emisión de JWT** al cambiar de sucursal invalida el token anterior (aceptable).
- **`favicon_url` en `tenants`:** deuda si crecen los ajustes → `tenant_settings`.

## 7. Handoff
- → **backend:** data-model (0011+0012), api-contract (RequireSuperAdmin, claim, select-branch,
  businessTenantID, M:N, sales/reports), retiro de `POST /tenants`.
- → **frontend:** frontend-flows (guard, /select-branch, admin super-admin-only, header,
  favicon) + menús por perfil (api-contract §8).
- → **devops:** sin infra nueva; recordar seed de **un** negocio + super admin.

## 8. Definición de listo — ✅ cerrado
- [x] Negocio único + propiedad del super admin (businessTenantID).
- [x] M:N `user_branches` (0012); `users.branch_id` retirado.
- [x] Sucursal activa por claim + `select-branch`; `sales.branch_id` derivado.
- [x] Admin (branches/favicon/users) solo super admin.
- [x] **MVP-A sin roles:** matriz de acceso por endpoint + menús por perfil (api-contract §7-8).
- Solo quedan afinamientos P2 no bloqueantes (seed de datos, visibilidad de "cambiar sucursal").
