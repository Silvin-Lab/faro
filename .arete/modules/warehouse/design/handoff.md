# Handoff a ingeniería — Almacén (warehouse)
_Autor: product-designer · Fecha: 2026-07-20 · Módulo: M8 · Gate: handoff completo_
_Fuentes: prd.md (aprobado) · wireframes.md · research.md · design-system Faro v0.3_

> Alcance de este handoff: **UI y comportamiento**. NO define modelo de datos ni contratos de API
> (eso es tech-spec, siguiente etapa). Las rutas son propuestas; tech puede ajustarlas.
> El módulo **no implementa roles** (v1 sin roles, ver §Roles).

## 0. Convenciones heredadas (no reinventar)
Toda la UI reusa lo ya construido en `faro-ui`:
- Componentes: `components/ui/{Card,Button,Input,FormField}.tsx`.
- Tokens Tailwind ya mapeados: `text-ink`, `text-muted`, `text-danger`, `bg-accent`,
  `bg-accent-strong`, `bg-surface`, `bg-bg`, `border-line`. (= design-system v0.3.)
- **Select** con la clase repetida hoy en `supplies`:
  `w-full rounded-md border border-line bg-surface px-3 py-2 text-sm text-ink outline-none focus:border-accent-strong`.
  → Recomiendo extraer a `components/ui/Select.tsx` en este módulo (ver §Design system).
- Layout de página: header row (`h1 text-2xl font-semibold text-ink` + acción a la derecha),
  `<p text-xs text-muted>` de ayuda, luego `Card`(s).
- Botón primario = lime + texto oscuro (`variant="primary"`, default). Secundarias = `ghost`/`outline`.
- Historial = tabla estilo `supplies/[id]/edit`: `thead` `border-b border-line text-xs uppercase
  tracking-wide text-muted`, `tbody` `divide-y divide-line`, cantidades negativas en `text-danger`,
  `tabular-nums` para números.

## 1. Navegación (F13) + decisión "Productos a comprar" (F4)

### 1.1 Sidebar
Introducir la **primera sección agrupada** del sidebar (el DS ya contempla "Section header").
`components/Sidebar.tsx` hoy es una lista plana bajo "Main Menu"; agregar un encabezado de sección
**"Almacén"** con estos items, en este orden:

| Orden | Label | Ruta propuesta | Requisito |
|---|---|---|---|
| 1 | Almacén | `/warehouse` | F1–F3 (landing + mín/máx) |
| 2 | **Productos a comprar** | `/warehouse/to-buy` | **F4 — DECISIÓN D1** |
| 3 | Compras | `/warehouse/purchases` | F5, F7, F13 |
| 4 | Salidas | `/warehouse/dispatches` | F8–F10, F13 |
| 5 | Mermas | `/warehouse/waste` | F11, F12, F13 |
| 6 | Proveedores | `/warehouse/suppliers` | F6 |

> **DECISIÓN DE DISEÑO (D1) — dónde vive "Productos a comprar":** el PRD (F13) solo nombra
> Compras/Mermas/Salidas y no ubicó la pantalla F4. La ubico como **ítem de menú propio** (posición
> 2), no dentro de Compras, porque F4 la define como "pantalla propia… herramienta de reposición".
> Además se refuerza con un **banner en la landing** y un **atajo desde Compras**. El orden del menú
> sigue el flujo real: ver qué falta → comprar. Documentado como decisión del diseñador.

Estado activo del item = pastilla lime (`bg-accent text-ink`), igual que hoy.

### 1.2 Roles — RESUELTO por Silvin (2026-07-20)
Decisión final del dueño de producto: **Almacén queda gated a `super_admin`** (`me.isSuperAdmin`),
igual que `supplies` hoy — no es "visible para todos" como sugería la recomendación de diseño
original. El sidebar solo debe mostrar la sección Almacén (y sus 6 submenús) a usuarios con
`isSuperAdmin`; el resto de perfiles no la ve, mismo patrón que `app/(app)/supplies/*`.

## 2. Especificación por pantalla

Para cada formulario, los **cuatro estados** son obligatorios (principio del sistema):
**vacío · carga · error · éxito**. Detalle abajo.

### 2.1 Almacén / Existencias (`/warehouse`) — F1, F2, F3
- **Contenido:** banner reposición (condicional) + Card con search/filtro + tabla de insumos.
- **Columnas:** Insumo · Stock (unidad base) · Mín (input) · Máx (input) · Estado (badge).
- **Mín/máx inline (D2):** inputs numéricos por fila; **persisten al blur o Enter**; feedback de
  guardado sutil (sin recargar toda la tabla). `min=0`, enteros. Regla: si ambos definidos,
  `máx ≥ mín` (validación suave: si falla, marcar el input en `border-danger` + mensaje corto).
- **Badge estado:** `stock ≤ mín (y mín definido)` → "Bajo mínimo" (`bg-[danger tint]`/texto danger);
  si no → "OK" (`bg-accent text-ink`). Si mín no definido → "Sin mínimo" (muted).
- **Banner reposición:** visible solo si `N (bajo mínimo) > 0`. Texto: "N insumos en o por debajo de
  su mínimo." + botón/enlace "Ver qué comprar →" a `/warehouse/to-buy`. Estilo Alert (ver §DS).
- **Estados:** loading (`text-muted "Cargando…"`), vacío catálogo (`"Aún no hay insumos en el
  catálogo."` + enlace a `/supplies`), error (`text-sm text-danger`).

### 2.2 Productos a comprar (`/warehouse/to-buy`) — F4
- **Query:** todos los insumos con `mín definido AND stock ≤ mín`.
- **Columnas:** Insumo · Stock · Mín · Faltan (`mín − stock`, ≥0) · Presentación (`packageName`) ·
  Acción.
- **Acción "Comprar"** → `/warehouse/purchases?supplyId=<id>` (prefill del insumo).
- **Estados:** vacío **feliz** (no danger): "Todo en orden. Ningún insumo está por debajo de su
  mínimo."; loading; error.

### 2.3 Compras (`/warehouse/purchases`) — F5, F7
- **Form (Card superior). Campos:**
  - Fecha — `type=date`, default hoy. Requerido.
  - Proveedor — Select de proveedores activos + enlace "＋ Nuevo proveedor" (a `/warehouse/suppliers/new`,
    idealmente volviendo con el proveedor creado seleccionado). Requerido.
  - Insumo — Select de insumos activos. Requerido. Prefill desde `?supplyId`.
  - Presentación — **texto derivado** del insumo: `packageName · +{packageContent} {unidad} por
    presentación`. No editable.
  - Precio de compra — `type=number`, pesos, `step=0.01 min=0`, **por presentación** (F5). Etiqueta
    debe decir "por presentación".
  - Cantidad — `type=number`, `step=1 min=1`, "presentaciones".
- **Cálculo en vivo** (bajo los campos, `text-xs text-muted`): "Entran {cantidad×packageContent}
  {unidad} al almacén · Total ${cantidad×precio}".
- **Botón** "Registrar compra" (`primary`, `loading`); **disabled** hasta proveedor+insumo+cantidad>0.
- **Éxito:** limpiar cantidad/precio, refrescar historial (aparece la fila nueva arriba). Sin toast
  obligatorio; el feedback es que aparece en el historial (patrón actual).
- **Historial (Card inferior):** Fecha · Insumo · Proveedor · Cant. (presentaciones) · Precio/pres ·
  Total · Quién. Vacío: "Sin compras registradas."

### 2.4 Proveedores (`/warehouse/suppliers`) — F6
- **Lista:** Card con `ul divide-y divide-line`; por fila: Nombre + (teléfono · correo) en muted +
  botón "Editar". Header con botón "Nuevo proveedor". Vacío: "Aún no hay proveedores."
- **Alta/Edición** (`Card max-w-lg`, patrón `supplies/new`): Nombre (requerido), Teléfono, Correo
  (`type=email`), Dirección. Botones "Guardar" (`loading`) + "Cancelar" (`ghost`). Error inline.
- **Baja:** seguir el patrón existente de "Desactivar/Activar" (soft) si tech modela `status`; si no,
  ofrecer "Eliminar" con `window.confirm` (patrón ya usado en Medidas de uso). Decisión final = tech
  según modelo; el diseño acepta ambas.

### 2.5 Salidas (`/warehouse/dispatches`) — F8, F9, F10
- **Form. Campos:** Fecha (`date`, default hoy), Insumo (Select, requerido), Cantidad (`number
  step=1 min=1`, unidad base), Sucursal destino (Select de sucursales, **requerido**).
- **Cálculo en vivo:** "Resta {n} {unidad} del almacén y suma {n} {unidad} a «{sucursal}»." Refuerza
  que el reflejo en sucursal es automático (F9/F10) — no hay doble captura.
- **Botón** "Registrar salida" (`primary`, `loading`); disabled hasta insumo+cantidad>0+sucursal.
- **Historial:** Fecha · Insumo · Cantidad (negativa, `text-danger`) · Sucursal destino · Quién.
  Vacío: "Sin salidas registradas."

### 2.6 Mermas (`/warehouse/waste`) — F11, F12  (D3)
- **Helper** (arriba del form, `text-xs text-muted`): explica los dos casos (almacén sin sucursal /
  sucursal con producto devuelto).
- **Form. Campos:** Fecha (`date`), Insumo (Select, requerido), Cantidad (`number step=1 min=1`),
  **Sucursal (opcional)** — Select cuyo **primer valor y default es "Sin sucursal (almacén central)"**,
  seguido de las sucursales; Motivo (`Input`, **requerido**, F12).
- **Botón** "Registrar merma" (`primary`, `loading`); disabled hasta insumo+cantidad>0+motivo.
- **Historial:** Fecha · Insumo · Cantidad (negativa) · Sucursal ("—" si ninguna) · Motivo · Quién.
  Vacío: "Sin mermas registradas."

### 2.7 Limpieza de `/supplies/[id]/edit` (F14)  — [ACTUALIZADO 2026-07-20: resolución N1]
- **Eliminar SOLO los 2 formularios de escritura** y su lógica de alta: "Entrada de compra"
  (`purchase`, `onPurchase`) y "Ajuste / merma" (`adjust`, `onAdjust`), junto con `createMovement`
  y el `<select>` de sucursal / campos de esos forms. Quitar imports/estados que queden realmente
  huérfanos tras retirarlos (p.ej. `listBranches`/`branches`, `selectClass` si ya no se usan).
- **MANTENER** la card "Historial de movimientos" (`movements`, `refreshMovements`, `listMovements`,
  `movementTypeLabel`, `formatSigned`, `dateTimeLabel`) **de solo lectura** — es la superficie de
  auditoría del inventario de la sucursal, con lo que **F10** queda satisfecho sin pantalla nueva.
  El historial ahora incluye `transfer` y `waste`: **etiquetarlos** en `movementTypeLabel`
  ("Entrada de almacén" / "Merma"), tech-spec §5.6.
- **Quedan:** cards "Datos", "Medidas de uso" y "Historial de movimientos" (solo lectura).
- **Agregar** bajo el header una línea de ayuda: "El registro de existencias (compras, salidas,
  mermas) ahora se gestiona en **Almacén**." con enlace a `/warehouse`. Evita que el usuario busque
  las funciones movidas.
- **Criterio de aceptación PRD:** la pantalla ya no muestra los **formularios** de compra ni
  ajuste/merma; el historial permanece, de solo lectura.

## 3. Comportamiento transversal
- **Feedback de éxito:** patrón actual = la operación aparece en el historial de la misma pantalla;
  limpiar campos de cantidad tras registrar. Toast opcional (no bloquea).
- **Errores:** inline en `text-sm text-danger` bajo el form (patrón `supplies`). Errores de API →
  `ApiError.message`; genérico si no es `ApiError`.
- **Números:** `tabular-nums`; cantidades de salida/merma se muestran **negativas y en danger** en los
  historiales (consistente con el ledger firmado actual).
- **Fechas:** captura `type=date`; display `dd/mm/aaaa` (o con hora donde ya se use `dateTimeLabel`).
- **Accesibilidad:** todo input con `<label>` (via `FormField`); foco visible; áreas táctiles ≥44px
  (tablet de mostrador); contraste AA (texto sobre lime siempre oscuro).
- **Responsive:** tablas con `overflow-x-auto` + `min-w-[...]` como en el historial actual; forms a
  ancho completo en tablet.

## 4. Aportes al design system (foundations)
Documentados en `foundations/design-system.md` (§ M8 Almacén). Nuevos patrones a formalizar:
1. **Select field** — extraer `selectClass` a `components/ui/Select.tsx` (mismos tokens).
2. **Alert / Callout banner** — banda con icono + texto + acción (reposición). Variante info/warning.
3. **Sidebar section group** — encabezado de sección muted + items (primer uso real del patrón que el
   DS ya describía).
4. **Status badge** — variantes "OK" (accent), "Bajo mínimo" (danger tint), "Sin mínimo" (muted).
5. **Date input** — `type=date` con los tokens de `Input`.

## 5. Trazabilidad requisitos → pantalla
| Req | Dónde |
|---|---|
| F1 almacén único | landing `/warehouse` (sin selector de sucursal) |
| F2 mín/máx por insumo | inline en landing (D2) |
| F3 stock propio de almacén | columna Stock en landing |
| F4 pantalla "Productos a comprar" | `/warehouse/to-buy` (ítem de menú propio, D1) |
| F5 compra por presentación | form Compras (precio/presentación + cálculo) |
| F6 CRUD proveedores | `/warehouse/suppliers` (+ new/edit) |
| F7 historial compras | Card historial en Compras |
| F8 salida | form Salidas |
| F9/F10 reflejo auditable en sucursal | copy en Salidas; visibilidad = historial de inventario de la sucursal (existente) |
| F11 merma sucursal opcional | form Mermas, select default "Sin sucursal" (D3) |
| F12 historial mermas | Card historial en Mermas |
| F13 sección Almacén + submenús | sidebar §1.1 |
| F14 limpieza supplies/edit | §2.7 (retira solo los 2 forms; el historial se queda de solo lectura) |

## 6. Notas abiertas para tech-spec (no son diseño)
- Reflejo en sucursal (F9/F10): cómo se modela el movimiento auditable (ver R2 del PRD) — no afecta
  la UI de Almacén, sí la del historial de inventario de la sucursal (ya existente).
- Mermas: reemplazo vs. coexistencia con `type=adjustment` (R3). La UI de Almacén es la misma en
  cualquier caso.
- Stock negativo en almacén (R4): si tech decide **bloquear** salidas/mermas sin existencias, el
  diseño lo soporta mostrando error inline ("Sin existencias suficientes en el almacén"); si decide
  **permitir** negativo (como hoy), no hay cambio de UI. Definir en tech-spec.
- Confirmar ubicación del menú Almacén por perfil de rol (§1.2).
</content>
