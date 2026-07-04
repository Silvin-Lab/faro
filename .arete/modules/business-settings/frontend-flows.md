# Flujos de frontend — M7 v2 business-settings (faro-ui)

_Fecha: 2026-07-03 · Decisiones: ADR-006 + **ADR-007** (negocio único) · Repo: faro-ui (Next.js 14 App Router)_

> Diseño de UX e integración; implementa frontend-engineer. Reutiliza `ImageUpload`,
> `lib/uploads.ts`, `lib/user-context.tsx`.

## 0. Sesión ampliada (`lib/auth.ts`, contexto)

```
type User    = { ...actual..., isSuperAdmin }
type Tenant  = { id, name, faviconUrl: string|null }        // null para super admin
type Branch  = { id, name }
type Session = { user, tenant: Tenant|null, branches: Branch[],
                 activeBranchId: string|null, mustSelectBranch: boolean }

getMe(): Session
login(email,pw): Session
selectBranch(branchId): { activeBranchId, branch }          // POST /auth/select-branch
```
El contexto expone `session` (user + tenant + branches + activeBranch). `app/(app)/layout.tsx`
la carga con `getMe`.

## 1. Guard post-login (selección de sucursal)

En `app/(app)/layout.tsx`, tras `getMe`:
- **super admin** → área de administración (no POS, no sucursal).
- **usuario operativo con `mustSelectBranch`** (>1 sucursal, sin activa) → redirigir a
  **`/select-branch`**.
- **con `activeBranchId`** (1 sucursal auto, o ya elegida) → entra al sistema.

### Pantalla `/select-branch` (nueva)
Lista `session.branches`; al elegir → `selectBranch(id)` (re-emite cookie) → navegar a
destino (POS/dashboard). Botón para **cambiar de sucursal** disponible luego desde el
header/menú (reabre esta selección).

## 2. Administración — **solo super admin** (se saca del entorno de usuarios de sucursal)

Menú de super admin (Sidebar) gana: **Sucursales**, **Ajustes/Negocio (favicon)**,
**Usuarios**. Los usuarios de sucursal **no** ven estas secciones.

- **`app/(app)/branches/…`** (o dentro de `/settings`): CRUD de sucursales (crear, renombrar,
  activar/desactivar, eliminar con manejo de `409 branch_in_use`).
- **`app/(app)/settings/…`**: card favicon (`ImageUpload` → `uploadImage` → `setFavicon`;
  preview; quitar).
- **`app/(app)/users/…`**: alta/edición con **asignación M:N de sucursales**
  (checkbox-list de sucursales activas → `branchIds`); columna "Sucursales" en el listado.
  Validar ≥1 sucursal para personal operativo.

Estas rutas se protegen en cliente por `isSuperAdmin` (y en servidor por `RequireSuperAdmin`).

## 3. Encabezado del POS

- Sucursal activa = `session.activeBranchId` → nombre desde `session.branches`.
- Composición (ADR-006 §D7): **`Faro. {activeBranch.name}`** (ej. "Faro. Vanta Centro").
- Control "Cambiar sucursal" (si `branches.length > 1`) que reabre `/select-branch`.
- La venta se etiqueta sola en servidor (claim); el POS no envía `branchId`.

## 4. Favicon por-tenant (runtime, sin cambios respecto a v1)

`components/FaviconManager.tsx` en el layout autenticado inyecta/reemplaza
`<link rel="icon">` con `imageSrc(tenant.faviconUrl)`; fallback a `/favicon.ico` si `null` o
super admin. No se usa `generateMetadata` (app cliente autenticada cross-origin, ADR-006 §D6).

## Archivos faro-ui afectados

| Archivo | Cambio |
|---------|--------|
| `lib/auth.ts` | `Session` (branches, activeBranchId, mustSelectBranch, tenant); `getMe/login`; `selectBranch`; `createUser/updateUser` con `branchIds` |
| `lib/user-context.tsx` | exponer `session` (tenant + branches + activeBranch) |
| `lib/branches.ts`, `lib/settings.ts` (nuevos) | CRUD sucursales / favicon (super admin) |
| `app/(app)/layout.tsx` | guard de selección; `FaviconManager` |
| `app/(app)/select-branch/page.tsx` (nuevo) | selección/cambio de sucursal |
| `components/Sidebar.tsx` | secciones de super admin (Sucursales, Negocio, Usuarios) ocultas a operativos |
| `components/FaviconManager.tsx` (nuevo) | inyección runtime |
| `app/(app)/branches`, `settings`, `users` | admin super-admin-only (CRUD + M:N) |
| `app/(app)/pos/page.tsx` | encabezado `Faro. {activeBranch.name}` + cambiar sucursal |
| `app/(app)/reports/…` | filtro/desglose por sucursal (según acceso — ver P1) |
