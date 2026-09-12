# Handoff a ingeniería — Repostería / Producción central (bakery)
_Autor: product-designer · Fecha: 2026-08-13 · Módulo: M10 · Gate: handoff completo_
_Fuentes: prd.md (aprobado) · wireframes.md · research.md · diseño técnico (Feature 2) · design-system Faro v0.3 (+M8, +M9)_

> Alcance de este handoff: **UI y comportamiento**. El modelo de datos, endpoints y transacciones ya
> están definidos en el **diseño técnico aprobado (Feature 2)** que retoma el tech-lead; aquí solo se
> especifica la experiencia sobre ese modelo. Las rutas son las propuestas del diseño técnico; tech
> puede ajustarlas.

## 0. Convenciones heredadas (no reinventar)
Toda la UI reusa lo ya construido en `faro-ui`:
- Componentes: `components/ui/{Card,Button,Input,Select,SearchableSelect,DateInput,FormField,Alert,StatusBadge,Modal}.tsx`.
- Tokens Tailwind: `text-ink`, `text-muted`, `text-danger`, `text-success`, `bg-accent`,
  `bg-accent-strong`, `bg-surface`, `bg-bg`, `border-line` (= design-system v0.3 +M8 +M9).
- Layout de página: header (`h1 text-2xl font-semibold text-ink` + acción a la derecha),
  `<p text-xs/sm text-muted>` de ayuda, luego `Card`(s). Contenedor de forms `max-w-3xl` (ver
  `dispatches`); tablas anchas `max-w` completo.
- Botón primario = lime + texto oscuro (`variant="primary"`); secundarias `ghost`/`outline`.
- Tabla/historial = patrón `warehouse/dispatches`: `overflow-x-auto` + `min-w-[…]`, `thead`
  `border-b border-line text-xs uppercase tracking-wide text-muted`, `tbody` `divide-y divide-line`,
  números `tabular-nums`, cantidades negativas en `text-danger`, fila de hoy con `font-semibold`
  (`isToday`).
- Fechas: captura `DateInput`; display con los helpers `dateLabel`/`isToday` de `lib/warehouse`
  (reutilizables o replicables en `lib/bakery`).
- Datos: `lib/bakery.ts` nuevo (estilo `lib/warehouse.ts`); `lib/auth.ts` agrega `Role='repostero'`
  y landing de repostero → `/bakery/queue` (ya previsto en el diseño técnico).

## 1. Navegación / Sidebar (F20, F21) — `components/Sidebar.tsx`
`Sidebar.tsx` ya soporta grupos (`NavGroup[]`) y menús por rol. Cambios:

### 1.1 Rol `repostero` — rama nueva (grupo plano, sin encabezados)
Landing directa `/bakery/queue` (sin `select-branch`, el repostero no tiene sucursal — F21).

| Orden | Label | Ruta | Icono (lucide) | Requisito |
|---|---|---|---|---|
| 1 | Cola de producción | `/bakery/queue` | `ChefHat` | F5–F11 |
| 2 | Insumos | `/bakery/supplies` | `Boxes` | F17 |
| 3 | Tendencia de venta | `/bakery/trend` | `TrendingUp` | F18, F19 |
| — | Mi cuenta | `/account` | `UserCog` | (común) |

### 1.2 `super_admin` — nuevo grupo "Repostería"
Añadir tras "Operación" (o "Almacén"):

| Label | Ruta | Icono | Requisito |
|---|---|---|---|
| Cola de producción | `/bakery/queue` | `ChefHat` | F5–F11 (opera en ausencia de repostero) |
| Stock de postres | `/bakery/stock` | `Cake` | F15 (todas las sucursales) |

La tendencia (F18) para super_admin vive en **Insights** (con filtro), no como ítem propio.

### 1.3 Roles de sucursal — ítems nuevos en el menú plano
- **branch_admin**: + `Pedidos a repostería` (`/bakery/orders`, `CakeSlice`) + `Stock de postres`
  (`/bakery/stock`, `Cake`). Tendencia dentro de su Insights (ya tiene el ítem Insights).
- **cashier / barista**: + `Pedidos a repostería` + `Stock de postres`. **Sin** tendencia (F19
  excluye cashier/barista).

Estado activo = pastilla lime (`bg-accent text-ink`), sin cambios. El gating de contenido lo hace el
servidor; el sidebar solo muestra los ítems por rol.

## 2. Especificación por pantalla
Los **cuatro estados** son obligatorios donde aplique: **vacío · carga · error · éxito**.

### 2.1 Pedidos a repostería (`/bakery/orders`) — sucursal — F3, F4, F6, F12–F14
- **Form "Nuevo pedido"** (Card superior):
  - Postre — `SearchableSelect`, opciones = productos `fulfillment_type='bakery'` (D1). Requerido.
    Si no hay ninguno → sustituir el select por aviso info (ver estados).
  - Cantidad — `number step=1 min=1`. Requerido.
  - Nota — `Input`, opcional.
  - Helper: "Se enviará a la repostería a nombre de « {sucursal activa} »." (F3: sin selector de
    sucursal; el back usa `activeBranch`).
  - Botón "Crear pedido" (`primary`, `loading`); disabled hasta postre + cantidad>0.
  - Éxito: limpiar postre/cantidad/nota; el pedido aparece arriba en "Mis pedidos".
- **Historial "Mis pedidos"** (Card inferior): Fecha · Postre · Pedido · **Surtido** (`n/m` + barra,
  §4.1) · Estado (`StatusBadge`, §4.3) · Nota · Acción.
  - Antigüedad (§4.2) como prefijo/indicador de la fecha en `pending`/`in_production` ≥2 días.
  - Acción por fila: `pending` → "Cancelar" (confirm; F12, solo si nada producido — el back valida).
    `shipped` → "Marcar recibido" (F14). Resto → "—".
- **Estados:** vacío catálogo (aviso info en el form) · histórico vacío ("Aún no has hecho
  pedidos.") · loading · error inline. Cancelar/recibir con `loading` en el botón de la fila; error
  inline por fila.

### 2.2 Stock de postres (`/bakery/stock`) — sucursal (la suya) / repostero+super_admin (todas) — F15
- Sucursal: tabla Postre · Disponible (`tabular-nums`, filas en `0` en `text-muted`). Sin acciones.
- **Super_admin / repostero** (todas las sucursales): misma pantalla con **filtro de sucursal**
  (`Select` "Todas" + sucursales) y columna extra **Sucursal**. Para "Todas" agrupar por
  sucursal+postre (o listar por sucursal). Reusa el mismo componente parametrizado por scope.
- Search opcional por nombre de postre.
- **Estados:** vacío ("Aún no tienes postres en stock. Aparecerán aquí cuando la repostería surta tus
  pedidos." / versión "todas": "Aún no hay stock de postres.") · loading · error inline.

### 2.3 Cola de producción (`/bakery/queue`) — repostero, super_admin — F5–F11
- **Filtros** (fila sobre la Card): Estado (`Select`, default **"Por surtir"** = pending +
  in_production) · Sucursal (`Select` "Todas" + sucursales) · Buscar postre (`Input`).
- **Tabla:** Antigüedad · Sucursal · Postre · Pedido · **Surtido** (`n/m` + barra) · **Falta**
  (`font-semibold`, = pedido−surtido) · Estado · Acción.
- **Orden:** más antiguo primero (FIFO) dentro del scope "por surtir".
- **Acción "Producir"** (solo `pending`/`in_production`) → **Modal** (§2.4).
- **Estados:** vacío feliz ("Cola al día. No hay pedidos por surtir.") · vacío por filtro ("Sin
  pedidos con estos filtros.") · loading · error inline.

### 2.4 Modal "Registrar producción" (`Modal`) — F7–F11
- **Contexto** (arriba): "{Postre} · para « {sucursal} »" + "Pedido: {m}  ·  Ya surtido: {s}  ·
  Falta: {m−s}".
- **Campo** "Cantidad producida ahora": `number step=1 min=1`, **default = falta**. Se permite
  `> falta`. Helper: "Puede ser menor a lo que falta (producción parcial)." (F8).
- **Preview en vivo** (D3), recalculado al teclear:
  - Insumos a consumir = receta del producto × cantidad (misma lógica de `rowQuantityBase` del
    editor de receta). Receta vacía → "Este postre no tiene receta; no se descontarán insumos."
  - "Acreditará {cantidad} u. de {postre} a « {sucursal} »."
  - Estado resultante: `s+cantidad ≥ m` → "El pedido quedará **Surtido**"; si no → "El pedido
    quedará **En producción**, faltarán **{m−s−cantidad}**".
- **Aviso insumos insuficientes** (`Alert warning`, condicional, D10): si algún insumo quedaría
  negativo, mostrar cuánto falta. **No bloquea** (permite negativo, criterio warehouse/R6). Si
  tech-spec decide **bloquear**, mostrar como error y `disabled` el botón — la UI soporta ambas.
- **Botones:** "Registrar producción" (`primary`, `loading`) + "Cancelar" (`ghost`). Éxito → cerrar
  modal, refrescar solo la fila afectada (progreso + estado) manteniendo filtros y scroll. Error de
  API → inline dentro del modal (`ApiError.message`; el back devuelve 409 `invalid_state` si el
  pedido ya no es producible — mostrar mensaje claro y refrescar la fila).

### 2.5 Insumos del almacén (`/bakery/supplies`) — repostero — F17 (solo lectura)
- **Reuso** de la tabla de `/warehouse` (Existencias) **sin escritura** (D5): columnas Insumo ·
  Stock · Mín (texto) · Estado (`StatusBadge`). Sin edición inline, sin "Ajustar existencia", sin
  enlaces a Compras/Salidas/Mermas.
- **Banner** bajo-mínimo: `Alert variant="info"` **sin acción** ("N insumos en o por debajo de su
  mínimo."). Condicional (oculto si N=0).
- Search + filtro de categoría reutilizados. Copy de cabecera: "Solo lectura. Las compras y ajustes
  los gestiona el administrador."
- Datos vía `GET /warehouse/stock` (ya accesible a repostero por el diseño técnico). **Regresión:**
  branch_admin/cashier/barista siguen en 403 sobre el resto del almacén (verificado en tests).
- **Estados:** loading · vacío catálogo · error inline.

### 2.6 Tendencia de venta de postres (`/bakery/trend` + sección en Insights) — F18, F19
- **Bloque reutilizable** (un componente, dos hosts): página `/bakery/trend` (repostero) y sección
  dentro de `/insights` (super_admin/branch_admin). Fuente: `GET /insights/bakery-trend`.
- **Filtro Sucursal**: visible para repostero y super_admin (`Select` "Todas" + sucursales);
  **oculto** para branch_admin (servidor acota a su sucursal). Comparación fija: semana en curso vs.
  anterior (sin selector de rango); mostrar las fechas de ambas semanas como subtítulo.
- **Tabla:** Postre · Sem. pasada (u) · Esta sem. (u) · Variación (§4.4). Orden por **Δ unidades
  desc**. Titular llano arriba (patrón _Tarjeta de insight_): "Más subieron: X (+n), Y (+n)."
- Nota al pie muted: "Compara ventas de la semana en curso vs. la anterior. Histórico, sin
  predicción."
- **Estados:** vacío ("Sin ventas de postres en las últimas dos semanas.") · loading · error inline.

### 2.7 Edición de producto (`/products/[id]/edit` y `/products/new`) — super_admin — F1, F2
- **Selector "¿Cómo se surte?"** en el Card "Editar producto" (tras Categoría, antes de Imagen):
  radio/`Select` con `branch_prepared` (default) / `bakery`. **Solo visible/editable por
  super_admin** (`me.isSuperAdmin`). Enviar como `fulfillmentType` en `updateProduct`/`createProduct`.
  Helper contextual bajo el selector según el valor.
- **Relabel del subtítulo de la Receta** (`RecipeSection`) según `fulfillmentType` (D7):
  - `branch_prepared`: "Insumos que consume una unidad de este producto. Se descuentan del stock al
    **vender**." (texto actual).
  - `bakery`: "Insumos que consume **producir** una unidad de este postre en la repostería central.
    Se descuentan del almacén central al registrar la producción (no al vender)."
  El editor de receta **no cambia** de estructura ni de lógica de captura.
- Nota: cambio de tipo con pedidos/stock existentes → regla de tech-spec (ver §6).

## 3. Comportamiento transversal
- **Feedback de éxito:** patrón M8 — la operación se refleja en la lista/fila de la misma pantalla;
  limpiar campos de cantidad tras registrar. Toast opcional (no bloquea).
- **Errores:** inline `text-sm text-danger` bajo el form/fila (patrón `warehouse`). API →
  `ApiError.message`; genérico si no es `ApiError`. El 409 `invalid_state` del `/produce` y del
  cancelar debe traducirse a un mensaje claro + refresco de la fila (el estado cambió por debajo).
- **Números:** `tabular-nums` en cantidades, progresos y variaciones.
- **Concurrencia (UX):** si dos personas trabajan la cola, tras un error de estado inválido
  **refrescar la fila** (o la cola) para reflejar el estado real (el back serializa con
  `SELECT FOR UPDATE`; la UI solo debe recuperarse con gracia).
- **Accesibilidad:** todo input con `<label>` (via `FormField`); foco visible; áreas táctiles ≥44px
  (tablet de mostrador); contraste AA (texto sobre lime siempre oscuro; badges con texto legible
  sobre su tinte). Barras de progreso con `aria-label` ("3 de 10 surtidos").
- **Responsive:** tablas `overflow-x-auto` + `min-w-[…]`; forms a ancho completo en tablet; el modal
  de producción `max-w-lg` (ya es el default de `Modal`).

## 4. Aportes al design system (foundations §M10)
Documentar en `foundations/design-system.md`. Patrones nuevos:
1. **Progreso de surtido (`n/m` + barra)** — celda con texto `despachado/pedido` (`tabular-nums`) +
   barra proporcional (`bg-accent` sobre `bg-bg`, radio `sm`), `aria-label` descriptivo. Reutiliza la
   _Ranking list con barra_ (M9) aplicada a "avance vs. objetivo". Estado 0 = barra vacía.
2. **Indicador de antigüedad (aging)** — icono reloj + "N días" en tono atención
   (`text-danger`/tinte suave, **no** `Alert`) para señalar prioridad en worklists sin gritar error.
   Umbral configurable (default 2 días).
3. **StatusBadge — variante `success`** — agregar a `components/ui/StatusBadge.tsx`:
   `success: 'bg-success/10 text-success'`. Mapeo de estados del pedido:
   `pending→muted · in_production→accent · shipped→success · received→success(+✓) · cancelled→danger`.
4. **Fila de variación semana vs. semana** — ▲ (`success`) / ▼ (`danger`) / `=` (`muted`) / "nuevo"
   (`accent`), con Δ absoluto (u) y % (omitido si base 0). Aplicación de _Comparación por segmentos_
   (M9) a una serie temporal; ordenar por el criterio accionable (Δ unidades).

## 5. Trazabilidad requisitos → pantalla
| Req | Dónde |
|---|---|
| F1 marcar producto "de repostería" | Selector en `/products/[id]/edit` (§2.7) |
| F2 receta = consumo por unidad producida | Relabel de Receta + preview del modal de producción |
| F3 crear pedido (postre, cantidad, nota) | Form `/bakery/orders` (§2.1) |
| F4 pedido nace `pending`, aparece en histórico y cola | `/bakery/orders` + `/bakery/queue` |
| F5 cola con todas las sucursales | `/bakery/queue` (§2.3) |
| F6 filtros cola / sucursal solo lo suyo | Filtros §2.3; scope por rol en §2.1 |
| F7–F9 registrar producción parcial/total, avance de estado | Modal §2.4 |
| F10/F11 doble efecto auditado al producir | Preview del modal §2.4 (efecto lo ejecuta el back) |
| F12/F13 cancelar solo `pending` sin producción | Acción "Cancelar" §2.1 |
| F14 marcar `received` informativo | Acción "Marcar recibido" §2.1 |
| F15 stock de postres por sucursal | `/bakery/stock` (§2.2) |
| F16 venta descuenta stock de postre | POS (existente) — no es pantalla nueva; el back descuenta |
| F17 stock de insumos (solo lectura) | `/bakery/supplies` (§2.5) |
| F18/F19 tendencia semana vs. semana + gating | `/bakery/trend` + Insights (§2.6) |
| F20/F21 rol repostero, sin sucursal, gating | Sidebar §1.1 + landing `/bakery/queue` |

## 6. Notas abiertas para tech-spec (no son diseño)
- **Antigüedad (aging):** umbral de "días sin atender" (default 2) — confirmar con negocio; es un
  parámetro visual, no afecta datos.
- **Stock insuficiente al producir (R6/D10):** ¿bloquea, permite negativo o solo avisa? La UI
  soporta las tres; definir la conducta canónica (consistente con warehouse).
- **Cambio de `fulfillment_type` con pedidos/stock existentes:** ¿se permite, se bloquea o se avisa?
  Regla de negocio para tech; la UI puede mostrar un aviso si tech expone el conteo de dependencias.
- **Stock de postres "todas las sucursales" (super_admin/repostero):** confirmar si se agrupa por
  sucursal o por postre — el diseño acepta ambas; se sugiere por sucursal para la lectura operativa.
- **Marcar recibido:** ¿es por pedido (como se diseñó) o podría necesitarse a nivel de línea? El PRD
  lo define por pedido (`shipped→received`); se asume por pedido.
</content>
