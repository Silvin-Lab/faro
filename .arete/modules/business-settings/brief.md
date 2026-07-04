# Brief — M7 v2 business-settings (negocio único: sucursales + favicon)

_Fecha: 2026-07-03 · Estado: 🎨 diseño listo (solo docs) · Depende de: M1, M4 (POS), M5 (reportes), uploads_

## Problema
Faro se opera como **un solo negocio por instancia**. El **super admin es el dueño de todo
el proyecto**; el personal pertenece a una o varias **sucursales**. Falta modelar
sucursales, atribuir ventas por sucursal, y personalizar el **favicon**.

## Alcance MVP (decisiones cerradas — ADR-006 + ADR-007)
1. **Negocio único:** una fila en `tenants`. El super admin (global) lo administra vía
   `businessTenantID()`. `POST /tenants` se retira.
2. **Sucursales:** CRUD **solo super admin** (nombre, activar/desactivar).
3. **Usuarios M:N:** el super admin crea usuarios y los asigna a **1+ sucursales**
   (`user_branches`, migración 0012, reemplaza `users.branch_id`).
4. **Sucursal activa de sesión:** post-login, si el usuario tiene >1 sucursal → pantalla de
   **selección**; si tiene 1 → directo. La activa viaja como **claim en el JWT**
   (`/auth/select-branch` la fija). Etiqueta `sales.branch_id` (derivado en servidor) y el
   POS muestra `Faro. {sucursal activa}`.
5. **Favicon:** una imagen por tenant (`tenants.favicon_url`, reusa uploads, inyección runtime),
   administrada **solo por super admin**.
6. **Reportes por sucursal:** filtro + desglose (bucket "Sin sucursal").

## Fuera de alcance / pendiente
- **Roles** (owner/barista por sucursal) y **qué ve/administra un usuario de sucursal** en
  products/categories/reports/loyalty → **pregunta abierta P1** (ver `open-questions.md`).
- FK compuesta `(tenant_id, branch_id)`; atributos de sucursal; `tenant_settings`.

## Entregables
`data-model.md` (0011 + 0012), `api-contract.md`, `frontend-flows.md`, `tech-spec.md`,
`open-questions.md`; `ADR-006` (base) + `ADR-007` (negocio único, M:N, sucursal activa).

## Estado
Núcleo cerrado y listo para handoff. Bloqueo parcial: **P1 (roles/acceso)** define el
alcance del usuario de sucursal en el resto del sistema.
