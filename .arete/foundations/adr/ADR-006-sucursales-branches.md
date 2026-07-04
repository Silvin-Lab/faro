# ADR-006 — Sucursales (branches) por negocio y resolución por usuario

_Fecha: 2026-07-02 (rev. 2026-07-03) · Estado: **parcialmente reemplazado por ADR-007** · Módulo: M7 business-settings_

> ⚠️ **ADR-007** (negocio único) reemplaza §D2 (sucursal por usuario, `users.branch_id`
> único → ahora membresía **M:N** `user_branches` + **sucursal activa de sesión**) y §D8
> (admin por cualquier usuario → ahora **solo super admin**). Siguen vigentes: §D1
> (entidad `branches`), §D3 (`sales.branch_id`), §D4-D6 (favicon), §D7 (encabezado
> `Faro. {sucursal}`).

## Contexto

Faro es multi-tenant con el modelo **1 negocio = 1 instancia lógica** (`tenant_id` en
cada tabla de negocio). Hasta hoy **no existe** ninguna noción de **sucursal**: el
negocio es una unidad monolítica. Los **clientes y la lealtad son a nivel tenant** y se
comparten entre sucursales por diseño (ver ADR-001 y ADR-005).

El negocio real (ej. "Vanta") opera **varias sucursales** físicas ("Vanta Centro",
"Vanta Zona Norte") bajo el mismo tenant. Necesita:

1. **Administrar sucursales** (nombre editable) desde el admin.
2. Que el **POS muestre la identidad de la sucursal** donde opera, en lugar del `Faro.`
   fijo actual.
3. Un **favicon por negocio** (una imagen por tenant) para personalizar la pestaña del
   navegador, **independiente** de la sucursal.
4. **Reportes por sucursal**: saber cuánto vendió cada local.

Requisito operativo confirmado con el humano: **todo se configura y mapea desde el
admin**. La sucursal de cada usuario/cajero se **asigna centralmente** en la pantalla de
Usuarios; el POS no debe pedir elección local.

## Decisiones

### D1 — Introducir `branches` como entidad de primera clase (tenant-scoped)

Nueva tabla `branches(id, tenant_id, name, status active|inactive, created_at,
updated_at)` con `UNIQUE(tenant_id, name)`. Es una **capa organizativa** colgada del
tenant; **no** subdivide el aislamiento de datos (clientes, lealtad, productos,
categorías siguen siendo tenant-level y compartidos entre sucursales).

### D2 — Resolución de sucursal **por usuario** (asignada desde el admin)

La sucursal se asocia al **usuario**: `users.branch_id` (nullable, FK a `branches` del
**mismo tenant**). El **POS toma la sucursal del usuario logueado**; **no hay selector en
el POS ni estado en `localStorage`**. La asignación se hace en la pantalla de **Usuarios**
del admin, en el **alta y la edición** de cada usuario.

**Por qué (cambio respecto a la propuesta inicial "por dispositivo"):** el humano quiere
que **todo se configure y mapee desde el admin**, como fuente de verdad única y auditable
en servidor. Atar la sucursal al usuario permite además **derivar `branch_id` en cada
venta** de forma confiable (servidor) y habilitar reportes por sucursal, sin depender de
un estado de cliente frágil.

**Alternativas consideradas:**

- **A) Por usuario, asignada en el admin (elegida).** Fuente de verdad en servidor;
  habilita atribución de ventas y reportes; cero estado en el cliente. Contra: si un
  cajero cubre otro local, el admin debe reasignar su sucursal (aceptable para el
  tamaño/operación actual).
- **B) Por dispositivo (localStorage).** Cero cambios de datos; el dispositivo "es" el
  local. Contra: estado no auditable en servidor, no permite derivar `branch_id` de venta
  de forma confiable, y contradice el requisito de "configurar todo desde el admin".
  Descartada.
- **C) Instancia/tenant por sucursal.** Rompería el modelo (clientes y lealtad deben
  compartirse entre sucursales del mismo negocio). Descartada.

`users.branch_id` es **nullable**: un usuario sin sucursal asignada opera sin identidad de
local (el POS muestra solo `Faro.`) y sus ventas quedan **sin sucursal**.

### D3 — Las ventas **registran `branch_id`** desde el MVP (derivado en servidor)

`sales.branch_id` (nullable, FK a `branches`). En `POST /sales`, el backend **deriva** el
`branch_id` del **usuario autenticado** (`users.branch_id`); **no** se acepta del cliente
(no confiar en el POS). Si el usuario no tiene sucursal, la venta se guarda con
`branch_id = NULL` ("Sin sucursal"). Esto habilita **reportes por sucursal** (filtro y
desglose, con bucket "Sin sucursal") desde el día 1.

### D4 — `favicon_url` como **columna en `tenants`**, no tabla de settings

El favicon es un **único escalar a nivel tenant**. `ALTER TABLE tenants ADD COLUMN
favicon_url text` es la opción más simple y sostenible (KISS). Una tabla
`tenant_settings` sería sobreingeniería para un campo. **Deuda documentada:** si más
adelante aparecen varios ajustes de marca por tenant (nombre comercial separado, logo,
colores, ticket), se migra a `tenant_settings(tenant_id, key, value)` o a columnas
dedicadas.

### D5 — Favicon reutiliza el módulo `uploads` existente

La imagen se sube con el **`POST /uploads`** ya existente (mismo mecanismo que
products/categories → devuelve `/files/...`) y la URL resultante se persiste en
`tenants.favicon_url` vía un endpoint de settings. No se crea un pipeline de subida nuevo.

### D6 — El favicon se inyecta en runtime (DOM), no vía `generateMetadata`

La app es un **cliente autenticado** y el favicon solo se conoce **después** de
`GET /auth/me`. La API vive en **otro origen** con cookie httpOnly, por lo que los
Server Components / `generateMetadata` del root layout **no** pueden resolver el favicon
del tenant en el render del servidor sin duplicar auth. Se decide un **componente cliente**
montado en el layout autenticado que inyecta/reemplaza `<link rel="icon">` en
`document.head` con la URL del tenant, con **fallback** al favicon estático por defecto
(root `metadata`). Ver `frontend-flows.md` §Favicon.

### D7 — Encabezado del POS: `Faro. {branch.name}`

El encabezado del POS es literal **`Faro. {branch.name}`** (ej. "Faro. Vanta Centro"). Si
el usuario **no tiene sucursal** asignada, se muestra solo **`Faro.`** (comportamiento
actual). Conserva el branding de plataforma y añade el local.

### D8 — Administración sin roles

Las sucursales y el favicon los administra **cualquier usuario del negocio** (Faro v1 no
tiene roles), consistente con Usuarios/Lealtad hoy. La restricción por rol queda para el
futuro módulo de roles.

## Consecuencias

- **Positivas:** sucursales como fuente de verdad en servidor; atribución de ventas y
  **reportes por sucursal** desde el MVP; favicon con superficie mínima (1 columna + 1
  endpoint); todo configurable desde el admin.
- **Negativas / deuda:**
  - Rotación de cajeros entre locales requiere reasignación manual de `users.branch_id`
    (aceptable a la escala actual; una futura "sucursal por dispositivo/turno" podría
    complementar).
  - Ventas de usuarios sin sucursal caen en el bucket "Sin sucursal" de reportes.
  - `favicon_url` en `tenants` habrá que migrarlo si crece el catálogo de settings.
- **Migración:** `0011_branches` — crea `branches`; añade `users.branch_id`,
  `sales.branch_id` y `tenants.favicon_url`. No destructiva; `down` revierte columnas y
  tabla (ver `data-model.md`).
- **Contrato:** `GET /auth/me` se **extiende** (aditivo) con `user.branchId` y un objeto
  `tenant` (`{id, name, faviconUrl}`, `null` para super admin), para que el POS componga
  el encabezado y el layout inyecte el favicon en la llamada que ya realiza. `POST /sales`
  deriva `branch_id` del usuario. Reportes ganan filtro/desglose por sucursal.
