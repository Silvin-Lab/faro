# ADR-007 — Negocio único, propiedad del super admin, membresía M:N y sucursal activa de sesión

_Fecha: 2026-07-03 · Estado: **aceptado** (decisiones cerradas con el humano) · Módulo: M7 v2 business-settings_
_Supersede: ADR-006 §D2 (sucursal por usuario, `users.branch_id` único) y §D8 (admin sin roles / cualquier usuario). Mantiene ADR-006 §D1 (entidad `branches`), §D3 (`sales.branch_id`), §D4-D6 (favicon)._

## Contexto

Aclaración del usuario: Faro se opera como **un solo negocio por instancia** (no hay
múltiples negocios). El **super admin es el dueño de todo el proyecto**; los demás usuarios
son personal de una o varias sucursales. Hoy el super admin es **global** (`tenant_id
NULL`, `is_super_admin`) y todo dato de negocio es **tenant-scoped**; ADR-006 había
modelado sucursal única por usuario (`users.branch_id`) administrable por cualquier usuario
del negocio. Ambas cosas cambian.

## Decisiones

### D1 — Modelo de **negocio único**
Existe **exactamente una** fila en `tenants` = "el negocio". Se **conserva** el esquema
multi-tenant (`tenant_id` en las tablas) por mínimo cambio y como red de seguridad, pero el
sistema corre **un solo tenant**. El endpoint de alta de negocios `POST /tenants` se
**retira** de este producto (el super admin ya no provisiona negocios). El "negocio" se
resuelve como esa única fila.

### D2 — Super admin = dueño global; resuelve **el tenant del negocio**
El super admin **permanece global** (`tenant_id NULL`, `is_super_admin=true`) y **no
pertenece a ninguna sucursal**. Los endpoints de administración resuelven el tenant del
negocio con un helper **`businessTenantID()`** (la única fila de `tenants`), en vez de
depender del `tenant_id` del caller.

- **Elegida (A):** super admin global + helper `businessTenantID()`. Ventaja: no muta la
  identidad del super admin, conserva el scoping por tenant intacto, mínimo cambio en el
  resto del sistema. Guard: si hubiera >1 tenant (legado de pruebas), el helper falla ruidoso
  → se designa/seed un único negocio.
- **Alternativa (B):** dar al super admin `tenant_id = negocio`. Descartada: mezcla la
  identidad del owner global con un tenant y complica el rol de super admin.

### D3 — Membresía **M:N** usuario↔sucursal (reemplaza `users.branch_id`)
Los usuarios **no** super admin pertenecen a **1 o más sucursales**. Se introduce
`user_branches(user_id, branch_id, tenant_id)` (M:N) y se **elimina** el `users.branch_id`
único que ADR-006 había propuesto en 0011. Migración **0012** (ver `data-model.md`).

### D4 — **Sucursal activa de sesión** vía claim en el JWT (re-emisión)
Tras el login, la sucursal activa de la sesión se guarda como **claim `activeBranchId` en
el JWT** (cookie httpOnly, coherente con ADR-002):
- Login carga las membresías. **1 sucursal** → el token se emite con `activeBranchId` ya
  fijado (entra directo). **>1** → token sin sucursal activa + `mustSelectBranch=true`; el
  cliente muestra la **pantalla de selección**; al elegir, `POST /auth/select-branch` valida
  la membresía y **re-emite** la cookie con `activeBranchId`.
- **Cambiar de sucursal** en sesión = volver a llamar `select-branch` (re-emite el token).
- **Por qué el JWT y no `branchId` del cliente en cada request:** la sucursal activa queda
  **validada en servidor al emitir** el token; `POST /sales` y el resto **no confían en el
  cliente** ni re-validan membresía por request. El cliente nunca puede etiquetar una venta
  con una sucursal a la que no pertenece. Alternativa (branchId por request validado contra
  membresía) descartada: más superficie de error, revalidación repetida.

### D5 — `POST /sales` deriva `branch_id` de la **sucursal activa de la sesión**
`sales.branch_id := claims.activeBranchId` (ya validado). El cliente **no** lo envía. Un
usuario operativo siempre tiene sucursal activa (tiene ≥1 membresía y la eligió); si no
hubiera activa, `POST /sales` responde `400 branch_required` (debe seleccionar sucursal). El
super admin no realiza ventas (no tiene sucursal).

### D6 — Autorización: administración **solo super admin**
Se mueven a **solo super admin** (fuera del entorno de usuarios de sucursal):
`branches` CRUD, `settings`/favicon, y **gestión de usuarios** (crear + asignar sucursales
M:N). Un usuario de sucursal **no** administra nada de esto. Middleware nuevo
`RequireSuperAdmin` (verifica `is_super_admin`).

## Consecuencias
- **Positivas:** modelo alineado con "un negocio, un dueño"; sucursal activa server-trusted;
  M:N soporta personal multi-sucursal; cambio de esquema acotado (una tabla + un drop de
  columna + claim). POS/venta/config siguen funcionando (scoping por tenant intacto).
- **Negativas / deuda:**
  - **Roles:** "quién administra products/categories/reports/loyalty" y "qué ve un usuario de
    sucursal" **no** está resuelto por la membresía M:N (que es pertenencia, no rol). Si se
    quiere owner/barista por sucursal, requiere un **sistema de roles** (hoy inexistente) →
    **pregunta abierta P1** (ver `open-questions.md`).
  - El "owner" tenant-scoped previo desaparece; sus usuarios pasan a ser personal de sucursal
    o se re-siembran (seed/migración de datos a confirmar).
  - Re-emitir el JWT al cambiar de sucursal invalida la sesión anterior (aceptable).
- **Migración:** **0012_user_branches** (M:N + drop `users.branch_id`); `sales.branch_id`,
  `branches`, `tenants.favicon_url` (0011) se conservan. `POST /tenants` se retira.
