# Task breakdown — Almacén (warehouse)
_Autor: project-manager · Fecha: 2026-07-20 · Módulo: M8 · Gate: tareas atómicas_
_Fuentes: prd.md (aprobado) · design/handoff.md · design/wireframes.md · tech-spec.md · ADR-008_
_Consumido por: backend-engineer · frontend-engineer_

> Convenciones: cada tarea es ejecutable por **un** especialista sin bloquearse esperando otra
> tarea del mismo tamaño. **Done** incluye las pruebas unitarias del propio dominio salvo que se
> indique una tarea de pruebas aparte. Tallas: **S** ≤ medio día · **M** ~1 día · **L** ~2 días.
> Rutas backend en `/Users/silvio/Projects/faro` (Go); frontend en `/Users/silvio/Projects/faro-ui`
> (Next.js). El módulo entero está **gated a `super_admin`** (handoff §1.2, tech-spec §1).

---

## Orden de alto nivel (camino crítico)

```
B1 migración ─► B2 modelo/skeleton ─┬─► B3 suppliers ───────────────┐
                                    ├─► B4 stock + to-buy ──┐        │
                                    │                       ├─► B5 compras ─┐
                                    │                       ├─► B6 salidas ─┼─► B8 wiring+gating ─► B10 tests e2e
                                    │                       └─► B7 mermas ──┘        │
                                    └─► B9 supplies guard/labels ─────────────────────┘

Frontend (arranca en paralelo al backend; integra contra endpoints al cerrar cada uno):
F1 Select ─┐
F2 Alert/Badge/Date ─┤            F3 lib/warehouse.ts (contra tech-spec §5) ─┐
                     ├─► F5 suppliers ─► F8 compras                          │
                     ├─► F6 landing   ├─► F9 salidas                         │ todas consumen F3
                     ├─► F7 to-buy    └─► F10 mermas                         │
F4 sidebar (independiente) · F11 limpieza supplies/edit (independiente) ─────┘
```

Reglas de dependencia real:
- La **migración y el modelo** van antes que cualquier endpoint.
- Los **endpoints** van antes que las pantallas que los consumen.
- El **catálogo de proveedores (B3/F5)** es prerrequisito de la pantalla de **Compras (F8)**
  (Select de proveedor + enlace "Nuevo proveedor").
- **Salidas (B6)** y **Mermas caso-b (B7)** reutilizan el upsert de `supply_branch_stock` de
  `internal/supplies`; **B6 antes que B7** (comparten el helper de la pata de sucursal).

---

## Backend (Go · `internal/warehouse` + migración)

### B1 — Migración `0019_warehouse` (up/down) · **M** · dep: ninguna
Crear `migrations/0019_warehouse.up.sql` / `.down.sql` según tech-spec §2, en este orden:
`suppliers` → `warehouse_stock` → `warehouse_movements` → **luego** `ALTER supply_movements`
(CHECK con `transfer`/`waste` + columna FK `warehouse_movement_id`). Aditiva, `IF NOT EXISTS`,
`BEGIN/COMMIT`, comentarios estilo 0015-0018.
- **Done:** `up` aplica sobre una DB con datos vivos de supplies/movements sin romper filas; los
  CHECK, UNIQUE, FK e índices (tech-spec §2.3) quedan creados; `down` revierte (columna FK,
  CHECK sin `transfer`/`waste`, drop de las 3 tablas) y documenta en el `.down.sql` que fallará
  si ya existen filas `transfer`/`waste` (como 0018). Corre en el runner de migraciones existente.

### B2 — Paquete `internal/warehouse` + modelo · **M** · dep: B1
Crear el skeleton del módulo (mismo patrón que `internal/supplies`): `model.go`, `store.go`,
`service.go`, `handler.go`, `response.go`. Definir structs: `Supplier`, `WarehouseStockItem`
(supply + stockBase + min/max + presentación + `status` derivado), `WarehouseMovement`
(purchase/dispatch/waste, firmado), y los DTO de request/response de tech-spec §5.
`NewService(pool)` + `Routes(...)` vacío montable.
- **Done:** compila; `Routes()` existe (aún sin handlers concretos); structs con tags JSON según
  contratos §5 (envelopes `{items}` / `{<recurso>}`, centavos, campos derivados). Sin lógica aún.

### B3 — Proveedores: CRUD (F6) · **M** · dep: B2
`store`/`service`/`handler` para `suppliers`: `GET /warehouse/suppliers` (`?status=active`
opcional), `POST` (name requerido, único por tenant → 409 `name_taken`), `PATCH /{id}` (parcial,
baja soft `status:"inactive"`). Scope por tenant. Errores estilo supplies.
- **Done:** los 3 endpoints responden con envelopes correctos; unicidad `(tenant_id,name)` → 409;
  baja soft funciona; tests unit del service (alta/duplicado/patch/baja); aislamiento por tenant.

### B4 — Stock de almacén + mín/máx + to-buy (F1-F4) · **M** · dep: B2
`GET /warehouse/stock` (todos los `supplies` LEFT JOIN `warehouse_stock`, con `stockBase`,
`minQuantity`, `maxQuantity`, presentación y `status` derivado `below_min|ok|no_min` en backend,
tech-spec §5.1); `PATCH /warehouse/stock/{supplyId}` (upsert lazy de mín/máx, validación dura
`max ≥ min` → 400); `GET /warehouse/to-buy` (min definido **y** `stock ≤ min`, con `missing`).
Exponer un helper `upsertWarehouseStock(tx, supplyId, deltaBase)` reutilizable por B5/B6/B7.
- **Done:** los 3 endpoints correctos; `status` derivado bien en los 3 casos; `max<min`→400;
  fila lazy se crea al primer PATCH; `to-buy` devuelve exactamente los insumos en/bajo mínimo;
  tests unit (status, validación, to-buy, upsert lazy); aislamiento por tenant.

### B5 — Compras: `purchase` (F5, F7) · **M** · dep: B3, B4
`POST /warehouse/purchases` (`{supplyId, supplierId, packages>0, unitCostCents≥0, date?}`) en
**una transacción**: insert `warehouse_movements(type=purchase, quantity_base=+packages*
supplies.package_content, supplier_id, packages, unit_cost_cents, created_by)` + upsert
`warehouse_stock`. `GET /warehouse/purchases` (historial `?from&to`, LIMIT 100 DESC, con
`supplierName`, `packages`, `unitCostCents`, `totalCents` derivado, `createdByName`). Mapeo de
`date` → `created_at` (tech-spec §4.4). Valida supply/supplier del tenant (400).
- **Done:** `quantity_base == packages*package_content`; upsert lazy crea fila de stock; supply/
  supplier ajenos→400; `totalCents` derivado correcto; backdating a mediodía UTC; tests unit.

### B6 — Salidas: `dispatch` (F8-F10) · **L** · dep: B4 · (recomendado antes de B7)
`POST /warehouse/dispatches` (`{supplyId, branchId, quantityBase>0, date?}`) ejecutando el flujo
de **4 efectos en una sola transacción** (tech-spec §3.2 / ADR-008 D2), con orden de bloqueo
determinista por `supply_id` (anti-deadlock, patrón `deductSupplies`):
1) `warehouse_movements(dispatch, −qty, branch_id)`; 2) `warehouse_stock −= qty`;
3) `supply_movements(transfer, +qty, branch_id, warehouse_movement_id)`; 4) `supply_branch_stock
+= qty`. Reutiliza el upsert de `supply_branch_stock` de `internal/supplies` (extraer/compartir
helper si hace falta). `GET /warehouse/dispatches` (historial con `branchName`, cantidad negativa,
`createdByName`). `branchId` requerido y del tenant (400 `invalid_branch`).
- **Done:** los 4 efectos ocurren atómicamente; rollback total si cualquiera falla; el `transfer`
  se crea con `warehouse_movement_id` y hereda el `created_at` del dispatch; `warehouse_stock−=qty`
  **y** `supply_branch_stock+=qty`; branch ajeno→400; stock puede quedar negativo (R4, no bloquea);
  tests unit de los 4 efectos + rollback.

### B7 — Mermas: `waste` con bifurcación (F11, F12) · **L** · dep: B4, B6
`POST /warehouse/waste` (`{supplyId, quantityBase>0, reason, branchId?, date?}`) que **bifurca por
`branchId`** (tech-spec §3.3):
- **sin** branch → `warehouse_movements(waste, −qty, branch_id=NULL, reason)` + `warehouse_stock
  −= qty`; `stockBase` de respuesta = stock del **almacén**.
- **con** branch → `supply_movements(waste, −qty, branch_id, reason)` + `supply_branch_stock −=
  qty` (mismo mecanismo que `insertMovement`); `stockBase` de respuesta = stock de **esa
  sucursal**. **No** toca `warehouse_stock`.
`GET /warehouse/waste` = **UNION** de `warehouse_movements`(waste, branch NULL) y `supply_movements`
(waste, branch set), ordenado por `created_at` DESC, LIMIT 100, con `branchName` (null→"—"),
`reason`, cantidad negativa, `createdByName`. `reason` requerido (400).
- **Done:** merma sin branch descuenta solo almacén; merma con branch descuenta solo esa sucursal
  y **no** toca el almacén; `reason` obligatorio→400; UNION del historial correcto y ordenado;
  respuesta indica el ledger de origen; tests unit de ambas ramas.

### B8 — Cableado del módulo + gating (F13 backend) · **S** · dep: B3, B4, B5, B6, B7
Registrar `warehouse.NewService(pool)` en `cmd/api/main.go` y montar
`r.Mount("/warehouse", warehouseSvc.Routes(...))` en `internal/server/server.go`. Aplicar
`authSvc.RequireSuperAdmin` a **todas** las rutas de `/warehouse` (lectura y escritura).
- **Done:** el módulo queda accesible bajo `/warehouse`; **toda** ruta exige `super_admin`
  (401/403 para el resto); smoke de arranque OK.

### B9 — `supplies`: guard de tipos + exposición de `transfer`/`waste` (§5.6, ADR-008 D4) · **S** · dep: B1
Confirmar/ajustar en `internal/supplies`: (1) `GET /supplies/{id}/movements` devuelve también
`type:"transfer"` y `type:"waste"` (sin cambio de struct, solo que el CHECK ya los admite);
(2) `POST /supplies/{id}/movements` (`CreateMovement`) **rechaza** cualquier type ≠
`purchase|adjustment` (incluidos `transfer`/`waste`/`sale`).
- **Done:** el listado incluye los tipos nuevos; el POST manual sigue rechazando `transfer`/`waste`
  (test que verifica el rechazo); nada más del módulo supplies cambia.

### B10 — Pruebas de integración / regresión (tech-spec §8) · **M** · dep: B5, B6, B7, B8, B9
Cubrir los escenarios cruzados que no caben en una sola tarea:
- **Regresión 100/10 galletas:** dispatch 100 a sucursal S (almacén −100, S +100) → waste con
  branch=S de 10 → verificar almacén = −100 (**sin** déficit fantasma) y `supply_branch_stock`
  de S = 90.
- **F9+F10 end-to-end:** tras un dispatch, `GET /supplies/{id}/movements` de la sucursal incluye
  el `transfer` con su fecha.
- **Gating:** usuario no `super_admin` → 401/403 en todas las rutas de `/warehouse`.
- **Aislamiento por tenant** en las 3 tablas nuevas.
- **Concurrencia:** dos dispatches concurrentes mismo insumo/sucursal sin deadlock, invariantes
  de ambos cachés consistentes con sus ledgers.
- **Done:** todos los escenarios pasan en CI; la migración se prueba sobre DB con datos vivos.

---

## Frontend (Next.js · `faro-ui`)

### F1 — `components/ui/Select.tsx` (design-system §DS.1) · **S** · dep: ninguna
Extraer el `selectClass` repetido (handoff §0) a un `Select` reutilizable con los mismos tokens.
Es prerrequisito de los forms de Compras/Salidas/Mermas/Proveedores y del filtro de la landing.
- **Done:** `Select` acepta options/label/value/onChange, estilos = tokens actuales; documentado
  en `foundations/design-system.md` §M8.1; usado al menos en una pantalla.

### F2 — Alert banner + Status badge + Date input (design-system §DS.2/4/5) · **S** · dep: ninguna
Tres primitivas presentacionales: **Alert/Callout** (icono + texto + acción, variantes
info/warning) para el banner de reposición; **Status badge** (variantes OK/accent,
"Bajo mínimo"/danger, "Sin mínimo"/muted); **Date input** (`type=date` con tokens de `Input`).
- **Done:** los 3 componentes renderizan sus variantes; documentados en design-system §M8; sin
  lógica de negocio (solo presentación).

### F3 — `lib/warehouse.ts` (cliente API + tipos) · **M** · dep: B3-B7 (contratos §5)
Cliente sobre `lib/api.ts` para: stock (GET), min/max (PATCH), to-buy (GET), suppliers
(GET/POST/PATCH), purchases (POST/GET), dispatches (POST/GET), waste (POST/GET) + tipos TS
espejo de los contratos (tech-spec §5). Se puede empezar contra el contrato y verificar al cerrar
cada endpoint backend.
- **Done:** funciones tipadas para cada endpoint; manejo de `ApiError`; centavos en/out;
  verificado contra los endpoints reales (no solo el contrato).

### F4 — Sidebar: sección "Almacén" gated (F13) · **S** · dep: ninguna (integra con rutas al existir)
En `components/Sidebar.tsx` introducir el **primer grupo con encabezado de sección** ("Almacén",
handoff §1.1) con los 6 items en orden: Almacén, Productos a comprar, Compras, Salidas, Mermas,
Proveedores. **Toda la sección visible solo si `me.isSuperAdmin`** (handoff §1.2), estado activo =
pastilla lime.
- **Done:** la sección aparece solo para super_admin; los 6 enlaces apuntan a las rutas correctas;
  item activo resaltado; patrón "section group" documentado en design-system.

### F5 — Proveedores: lista + new + edit (F6) · **M** · dep: F3, B3
`/warehouse/suppliers` (lista con nombre + tel·correo muted + Editar; header "Nuevo proveedor";
vacío "Aún no hay proveedores."), `/warehouse/suppliers/new` y `/warehouse/suppliers/[id]/edit`
(`Card max-w-lg`: Nombre requerido, Teléfono, Correo `type=email`, Dirección; Guardar/Cancelar;
baja soft "Desactivar/Activar"). Cuatro estados (vacío/carga/error/éxito).
- **Done:** CRUD completo funcionando contra B3; validación email; baja soft; los 4 estados;
  prerequisito de F8 satisfecho (el Select de proveedor puede poblarse).

### F6 — Almacén / Existencias landing (`/warehouse`, F1-F3) · **L** · dep: F1, F2, F3, B4
Banner de reposición (Alert, solo si N>0, enlace a `/warehouse/to-buy`) + Card con search + filtro
de categoría (reusa el de `/supplies`) + tabla: Insumo · Stock · Mín (input inline) · Máx (input
inline) · Estado (badge). Mín/máx **persisten al blur/Enter** (PATCH) con feedback sutil, sin
recargar toda la tabla; validación suave `máx≥mín` (border-danger + mensaje). Estados loading/
vacío-catálogo/error.
- **Done:** tabla con stock y badge derivado; edición inline de mín/máx persiste vía B4; banner
  condicional correcto; validación suave; los estados; áreas táctiles ≥44px.

### F7 — Productos a comprar (`/warehouse/to-buy`, F4) · **S** · dep: F3, B4
Tabla: Insumo · Stock · Mín · Faltan (`mín−stock`≥0) · Presentación (`packageName`) · Acción
"Comprar" → `/warehouse/purchases?supplyId=<id>`. Vacío **feliz** (no danger): "Todo en orden…".
Loading/error estándar.
- **Done:** lista exactamente los insumos en/bajo mínimo (de B4 `to-buy`); "Comprar" navega con
  prefill; vacío feliz; estados.

### F8 — Compras (`/warehouse/purchases`, F5, F7) · **L** · dep: F1, F3, F5, B5
Form (Card superior): Fecha (date, default hoy) · Proveedor (Select activos + enlace "＋ Nuevo
proveedor" a `/warehouse/suppliers/new`) · Insumo (Select, prefill desde `?supplyId`) ·
Presentación (texto derivado no editable) · Precio de compra (number, pesos, "por presentación") ·
Cantidad (presentaciones). **Cálculo en vivo**: unidades base = cantidad×packageContent, total =
cantidad×precio. Botón disabled hasta proveedor+insumo+cantidad>0. Historial (Card inferior):
Fecha·Insumo·Proveedor·Cant·Precio/pres·Total·Quién; vacío "Sin compras registradas."
- **Done:** alta de compra vía B5; prefill de `supplyId`; cálculo en vivo correcto; enlace a nuevo
  proveedor; historial se refresca con la fila nueva arriba; los 4 estados.

### F9 — Salidas (`/warehouse/dispatches`, F8-F10) · **M** · dep: F1, F3, B6
Form: Fecha (date) · Insumo (Select) · Cantidad (number, unidad base) · Sucursal destino (Select
de sucursales — usa `lib/branches` existente, **requerida**). **Cálculo en vivo**: "Resta {n}
{unidad} del almacén y suma {n} a «{sucursal}»." Botón disabled hasta insumo+cantidad>0+sucursal.
Historial: Fecha·Insumo·Cantidad(negativa,danger)·Sucursal·Quién; vacío "Sin salidas registradas."
- **Done:** alta de salida vía B6; copy de reflejo automático visible; historial correcto; los 4
  estados.

### F10 — Mermas (`/warehouse/waste`, F11, F12) · **M** · dep: F1, F3, B7
Helper de 2 casos arriba del form. Campos: Fecha · Insumo (Select) · Cantidad · **Sucursal
(opcional)** — Select cuyo **primer valor y default es "Sin sucursal (almacén central)"** seguido
de las sucursales; Motivo (Input, **requerido**). Botón disabled hasta insumo+cantidad>0+motivo.
Historial: Fecha·Insumo·Cantidad(negativa)·Sucursal("—" si ninguna)·Motivo·Quién; vacío "Sin
mermas registradas."
- **Done:** merma sin sucursal (default) y con sucursal vía B7; motivo obligatorio; historial UNION
  correcto con "—" para almacén; los 4 estados.

### F11 — Limpieza de `/supplies/[id]/edit` (F14) · **M** · dep: ninguna (backend intacto)
Eliminar **solo los 2 formularios de escritura**: "Entrada de compra" (`onPurchase`) y
"Ajuste/merma" (`onAdjust`), con sus estados/handlers de alta (`createMovement`, y el `<select>`
de sucursal y campos de esos forms). **Mantener** la card "Historial de movimientos"
(`refreshMovements`/`listMovements`/`movementTypeLabel`/`formatSigned`/`dateTimeLabel`) **de solo
lectura** — sigue mostrando el historial de inventario de la sucursal, con lo que **F10** queda
satisfecho sin pantalla nueva (resolución de N1 por Silvin). Como el historial se conserva y ahora
incluye los tipos nuevos, **etiquetar `transfer` ("Entrada de almacén") y `waste` ("Merma")** en
`movementTypeLabel` (tech-spec §5.6). Quitar solo los imports/estados que queden realmente
huérfanos tras retirar los forms (p.ej. `branches`/`listBranches` si ya no se usan). Dejar Cards
"Datos" y "Medidas de uso". Añadir bajo el header la línea de ayuda con enlace: "El registro de
existencias (compras, salidas, mermas) ahora se gestiona en **Almacén**." → `/warehouse`.
- **Done:** la pantalla ya no muestra los 2 **formularios** de compra/ajuste; el **historial de
  movimientos permanece, de solo lectura**, y etiqueta `transfer`/`waste`; sin código muerto ni
  imports rotos; compila y lint pasa; la nota con enlace aparece.

---

## Notas abiertas / a reconciliar (no bloquean el arranque, pero avisarlas)
- **N1 — [RESUELTA por Silvin, 2026-07-20] Visibilidad de F10 tras la limpieza de F14.** Se
  retiran de `/supplies/[id]/edit` **solo los formularios** de compra y ajuste/merma; la **tabla
  de "Historial de movimientos" se conserva ahí, de solo lectura**. Así el `transfer` (y el
  `waste` de sucursal) siguen visibles en el historial de la sucursal y **F9/F10 quedan
  satisfechos sin pantalla nueva**. El historial conservado debe etiquetar `transfer`
  ("Entrada de almacén") y `waste` ("Merma") en `movementTypeLabel` (recogido en F11).
- **N2 — QA/E2E** (qa-engineer) y **deploy de la migración** (devops, tech-spec §7) son etapas
  posteriores; no se detallan aquí (este breakdown cubre backend + frontend de implementación).
