# Contrato de API — M7 v2 business-settings

_Fecha: 2026-07-03 · Decisiones: ADR-006 + **ADR-007** (negocio único) · Estilo: REST/JSON, camelCase, cookie de sesión_

Convenciones: sesión por cookie httpOnly (JWT); errores
`{ "error","message" }`; recurso único envuelto, listas como `{ "items": [...] }`.

**Autorización (ADR-007 §D6):**
- **`RequireSuperAdmin`** (nuevo middleware, verifica `is_super_admin`): administración de
  `branches`, `settings`/favicon y `users` (crear + asignar sucursales).
- **`RequireSession`**: resto (POS, ventas, catálogo, reportes, `/auth/*`).
- El super admin resuelve **el tenant del negocio** con `businessTenantID()` (única fila de
  `tenants`), no con su propio `tenant_id` (que es NULL).

---

## 1. Sucursales — CRUD (`/branches`, **solo super admin**)

### `GET /branches`  → `{ "items": [ {id,tenantId,name,status,createdAt,updatedAt} ] }`
`?status=active` para el poblado de selects. (Los usuarios de sucursal **no** llaman este
endpoint; reciben sus sucursales vía `/auth/me`, ver §4.)

### `POST /branches`  `{ "name" }` → `201 { "branch" }` · `409 name_taken`
### `PATCH /branches/{id}`  `{ "name"?, "status"? }` → `200 { "branch" }` · `404` · `409 name_taken`
### `DELETE /branches/{id}` → `204` · `409 branch_in_use` (hay membresías o ventas). Preferir `PATCH status=inactive`.

---

## 2. Ajustes / favicon (`/settings`, **solo super admin**)

### `GET /settings` → `{ "tenant": {id,name}, "faviconUrl": "/files/…"|null }`
### `PUT /settings/favicon` `{ "faviconUrl" }` → `200 { "faviconUrl" }` (validar `/files/*`)
### `DELETE /settings/favicon` → `204`

Imagen subida con el `POST /uploads` existente (reutilizado).

---

## 3. Usuarios (`/users`, **solo super admin**) — membresía M:N

### `POST /users`  (alta)
```
Request:  { "email","password","name", "branchIds": ["<uuid>", ...] }   // 1+ sucursales
201       { "user": { ...campos..., "branches": [ {id,name} ... ] } }
400 validation_error   -> branchIds vacío (se exige ≥1 para personal operativo)
404 branch_not_found   -> alguna branch no existe o no es del negocio
409 email_taken
```

### `PATCH /users/{id}`  (edición / reasignación de sucursales)
```
Request:  { "branchIds"?: ["<uuid>", ...], ...otros campos... }
200       { "user": { ..., "branches":[...] } }   // branchIds reemplaza el set completo
400 · 404 not_found | branch_not_found
```

### `GET /users` → cada item incluye `branches: [{id,name}]`.

> El super admin **no** se asigna a sucursales (no es personal operativo). Un usuario
> operativo debe tener **≥1** sucursal.

---

## 4. Login y sesión — sucursal activa (ADR-007 §D4)

### `POST /auth/login`  (extendido)
```
200 {
  "user":   { ..., "isSuperAdmin" },
  "tenant": { "id","name","faviconUrl" } | null,      // null para super admin
  "branches": [ {id,name} ],                          // membresías (vacío para super admin)
  "activeBranchId": "<uuid>" | null,                  // ya fijado si branches.length == 1
  "mustSelectBranch": true|false                      // true si branches.length > 1 y sin activa
}
```
- `branches.length == 1` → el servidor emite el JWT con `activeBranchId` ya puesto (entra directo).
- `branches.length > 1` → `mustSelectBranch=true`, sin `activeBranchId`; el cliente va a la
  pantalla de selección.
- super admin → `branches=[]`, `activeBranchId=null`, `mustSelectBranch=false`.

### `POST /auth/select-branch`  (nuevo, `RequireSession`)
```
Request:  { "branchId": "<uuid>" }
200       { "activeBranchId": "<uuid>", "branch": {id,name} }   // re-emite la cookie (JWT con claim)
403 not_member   -> la branch no está en las membresías del usuario
404 branch_not_found
```
Cambiar de sucursal = volver a llamar. El claim `activeBranchId` viaja en el JWT
(server-trusted); ningún otro endpoint acepta `branchId` del cliente.

### `GET /auth/me`  (extendido)
```
200 {
  "user":   { ..., "isSuperAdmin" },
  "tenant": { "id","name","faviconUrl" } | null,
  "branches": [ {id,name} ],
  "activeBranchId": "<uuid>" | null,
  "mustSelectBranch": true|false
}
```
Con esto el cliente compone el encabezado (`Faro. {activeBranch.name}`), inyecta el favicon
y decide si mostrar la pantalla de selección — todo en la llamada que el layout ya hace.

---

## 5. Ventas — `POST /sales` deriva `branch_id` de la **sesión activa**

- `sales.branch_id := claims.activeBranchId` (validado al emitir el token). El cliente **no**
  envía `branchId`; si lo envía, se ignora.
- Sin sucursal activa (usuario operativo que no eligió) → `400 branch_required`.
- Super admin no crea ventas.

---

## 6. Reportes — filtro y desglose por sucursal (`RequireSession`)

- `?branchId=<uuid>` acota; `?branchId=none` → bucket `sales.branch_id IS NULL`.
- Desglose `byBranch: [ {branchId, branchName, totalCents, salesCount} ]` (bucket `null` =
  "Sin sucursal"). Quién accede a reportes → **pregunta abierta P1** (super admin vs sucursal).

---

## Resumen de superficie (v2)

| Método | Ruta | Auth | Cambio |
|--------|------|------|--------|
| GET/POST/PATCH/DELETE | `/branches[/{id}]` | **super admin** | nuevo |
| GET/PUT/DELETE | `/settings[/favicon]` | **super admin** | nuevo |
| POST/PATCH/GET | `/users[/{id}]` | **super admin** | `branchIds` (M:N) |
| POST | `/auth/select-branch` | sesión | **nuevo** (fija sucursal activa, re-emite JWT) |
| POST | `/auth/login` | — | `branches`, `activeBranchId`, `mustSelectBranch`, `tenant` |
| GET | `/auth/me` | sesión | idem login |
| POST | `/sales` | sesión | deriva `branch_id` del claim `activeBranchId` |
| GET | `/reports…` | sesión | `?branchId` + `byBranch` |
| — | `POST /tenants` | — | **retirado** (negocio único) |
| POST | `/uploads` | super admin | reutilizado (favicon) |

---

## 7. Matriz de acceso (MVP-A, sin roles)

Dos perfiles: **super admin** (dueño global; resuelve el negocio con `businessTenantID()`) y
**usuario de sucursal** (personal operativo; tenant propio + sucursal activa). No hay roles
intermedios. **Resolución de tenant:** helper `resolveTenant(caller)` = `caller.tenant_id`
si existe, si no `businessTenantID()` para el super admin (reemplaza el viejo `targetTenant`).
Todos los datos apuntan al **único negocio**; el único read acotado a **sucursal** es
`GET /sales`.

| Módulo | Endpoints | Lectura | Escritura | Scoping de datos |
|--------|-----------|---------|-----------|------------------|
| **auth** | `/auth/login`,`/logout`,`/me`,`/select-branch` | ambos | ambos | sesión/usuario |
| **users** | `/users` GET/POST/PATCH (+`branchIds`) | super-admin | super-admin | negocio |
| **branches** | `/branches` CRUD | super-admin | super-admin | negocio |
| **settings** | `/settings`, `/settings/favicon` | super-admin | super-admin | negocio |
| **products** | `GET /products` · `POST/PATCH/DELETE` | **ambos** (GET) | super-admin | negocio (catálogo compartido) |
| **categories** | `GET /categories` · `POST/PATCH/DELETE` | **ambos** (GET) | super-admin | negocio (compartido) |
| **loyalty** | `GET` promos/estado cliente · CRUD promos | **ambos** (GET) | super-admin (CRUD) | negocio (compartido) |
| **customers** | `/customers` buscar/crear/estado | usuario-de-sucursal | usuario-de-sucursal | negocio (compartido) |
| **sales** | `POST /sales` · `GET /sales` (ventas del día) | usuario-de-sucursal | usuario-de-sucursal | **sucursal activa** (forzado por servidor) |
| **reports** | `/reports…` | super-admin | — | negocio (todas las sucursales; filtro `?branchId`) |
| **uploads** | `POST /uploads` | super-admin | super-admin | negocio (imágenes de catálogo + favicon) |

**Scoping del POS (usuario de sucursal):**
- `GET /sales` ("ventas del día") se **fuerza en servidor** a `branch_id = claims.activeBranchId`
  (`WHERE tenant AND branch_id = <activa>`). **No** acepta `branchId` del cliente y **no** ve
  otras sucursales.
- `POST /sales` etiqueta `branch_id = claims.activeBranchId` (ADR-007 §D5).
- Catálogo, lealtad (lectura) y clientes son **tenant-level compartidos**: el usuario ve todo
  el negocio (no se filtran por sucursal). Solo `sales` es branch-scoped.
- El usuario de sucursal **no** accede a `/reports`, ni a escritura de products/categories,
  ni a CRUD de loyalty, ni a branches/settings/users/uploads → responden `403 forbidden`
  (o `404` para no filtrar) bajo `RequireSuperAdmin`.

**Notas de implementación:**
- Módulos con lectura **ambos** + escritura **super-admin** (products, categories, loyalty):
  los GET usan `RequireSession` + `resolveTenant`; las mutaciones usan `RequireSuperAdmin`.
- El super admin tiene `tenant_id NULL`: los módulos que hoy usan `tenantOf` deben pasar a
  `resolveTenant` para que el super admin resuelva `businessTenantID()` en products/categories/
  loyalty(write)/reports/uploads. `customers`/`sales` siguen con `tenantOf` (solo usuario de
  sucursal).

---

## 8. Menú por perfil (frontend)

**Super admin** (no opera POS): `Productos`, `Categorías`, `Lealtad`, `Usuarios`,
`Sucursales`, `Reportes`, `Negocio` (Ajustes/favicon). Se elimina "Nuevo negocio"
(`/tenants` retirado).

**Usuario de sucursal:** **solo `Punto de venta`** (POS). En el header del POS: identidad
`Faro. {sucursal activa}`, "Ventas del día" (GET /sales de su sucursal) y, si tiene >1
sucursal, "Cambiar sucursal" (`/select-branch`). Nada de catálogo/reportes/admin en el menú
(aunque el POS **lea** catálogo/lealtad/clientes por API).
