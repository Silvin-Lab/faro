# Wireframes — Repostería / Producción central (bakery)
_Autor: product-designer · Fecha: 2026-08-13 · Módulo: M10 · Fuente: prd.md (aprobado) · research.md · diseño técnico (Feature 2)_

Wireframes de baja fidelidad (estructura, jerarquía y estados). El look final se hereda del design
system de Faro (BrightPOS lime) y de los componentes existentes (`Card`, `Button`, `Input`,
`Select`, `SearchableSelect`, `DateInput`, `FormField`, `Alert`, `StatusBadge`, `Modal`, tablas de
historial). Specs de estados/comportamiento y tokens en `handoff.md`.

## Mapa de navegación (rutas propuestas)
```
Sucursal (branch_admin / cashier / barista)
├─ Pedidos a repostería   /bakery/orders     → crear pedido + histórico propio
└─ Stock de postres       /bakery/stock       → stock terminado de MI sucursal

Repostero (rol nuevo, sin sucursal · landing = /bakery/queue)
├─ Cola de producción     /bakery/queue       → pedidos de TODAS las sucursales + registrar producción
├─ Insumos                /bakery/supplies    → stock del almacén central (solo lectura)
└─ Tendencia de venta     /bakery/trend        → tendencia de postres (bloque reutilizable)

Super admin
├─ (grupo "Repostería")   /bakery/queue        → misma cola (opera en ausencia del repostero)
│                         /bakery/stock         → stock de postres de TODAS las sucursales
├─ Insights               /insights             → + sección/tarjeta "Tendencia de postres"
└─ Productos → editar     /products/[id]/edit   → selector "de repostería" + relabel de receta
```

Flujo principal (loop de reposición):
```
Sucursal: crear pedido (pending)
   → Repostero: cola → Registrar producción (parcial o total)
        → baja insumos (almacén central) + acredita stock del postre (sucursal)
        → in_production (parcial) … → shipped (completo)
   → Sucursal: Marcar recibido (received, informativo)   ·   Cancelar (solo pending, sin producción)
```

Ciclo de estados (badge en toda la UI):
```
pending ──producción parcial──► in_production ──se completa──► shipped ──sucursal──► received
   │
   └──cancelar (solo si nada producido)──► cancelled
```

---

## Pantalla 1 — Sucursal: crear pedido a repostería (`/bakery/orders`)
Cubre F3, F4, F6 (solo lo suyo), F12/F13 (cancelar), F14 (marcar recibido). Patrón form + historial
en la misma ruta (D5 de M8).

```
┌───────────────────────────────────────────────────────────────────────────┐
│  Pedidos a repostería                                                       │
│  Pide postres a la repostería central. Se surten a « Sucursal Centro ».     │
│                                                                             │
│  ┌── Card: Nuevo pedido ─────────────────────────────────────────────────┐ │
│  │  Postre     [ Buscar postre…                        ▾ ] (searchable)   │ │
│  │  Cantidad   [ 0 ]                                                       │ │
│  │  Nota (opcional) [ Ej. para el fin de semana                        ]  │ │
│  │  ──────────────────────────────────────────────────────────────────   │ │
│  │  Se enviará a la repostería a nombre de « Sucursal Centro ».           │ │  ← helper (F3)
│  │  [ Crear pedido ]                                                       │ │
│  └────────────────────────────────────────────────────────────────────────┘ │
│                                                                             │
│  ┌── Card: Mis pedidos ──────────────────────────────────────────────────┐ │
│  │ FECHA      POSTRE        PEDIDO  SURTIDO       ESTADO         NOTA  ···  │ │
│  │ 13/08 hoy  Cheesecake    10      ███░░░ 3/10   ●En producción  —   [⋯]  │ │
│  │ 12/08      Brownie       6       ██████ 6/6    ●Surtido        fin [Recibir]
│  │ 11/08 ⏱2d  Flan          4       ░░░░░░ 0/4    ●Pendiente      —   [Cancelar]
│  │ 10/08      Galleta       12      ██████ 12/12  ●Recibido ✓     —    —   │ │
│  │ 09/08      Pay           5       ░░░░░░ 0/5    ●Cancelado      —    —   │ │
│  └────────────────────────────────────────────────────────────────────────┘ │
└───────────────────────────────────────────────────────────────────────────┘
```
- **Postre**: `SearchableSelect` con **solo** productos `fulfillment_type='bakery'` (D1). Requerido.
- **Cantidad**: `number step=1 min=1`. **Nota**: `Input`, opcional.
- **[Crear pedido]** deshabilitado hasta postre + cantidad>0. Al crear → nace `pending`, aparece
  arriba en "Mis pedidos" y limpia el form (feedback = aparece en la lista, patrón M8).
- **SURTIDO**: `despachado/pedido` + barra proporcional (D2). Pendiente = `0/n` barra vacía.
- **ESTADO**: `StatusBadge` (D8). **⏱2d** = antigüedad de pedidos `pending`/`in_production` ≥2 días
  (D4), tono atención, informativo para la sucursal ("ya lo pediste hace 2 días").
- **Acción por fila** (columna final):
  - `pending` → **Cancelar** (F12; confirma en `window.confirm`/Modal). Sale como `cancelled`.
  - `shipped` → **Marcar recibido** (F14; informativo, no mueve stock).
  - `in_production` / `received` / `cancelled` → sin acción ("—").
- **Estados**:
  - _Vacío catálogo_ (no hay postres de repostería): en el form, en vez del select, aviso "Aún no
    hay postres de repostería. Pídele al administrador que marque los productos que surte la
    central." (info, sin danger).
  - _Histórico vacío_: "Aún no has hecho pedidos."
  - _Loading_: "Cargando…". _Error_ de carga/submit: inline `text-sm text-danger`.

---

## Pantalla 2 — Sucursal: stock de postres recibido (`/bakery/stock`)
Cubre F15 (la sucursal ve **su** stock). Vista simple de solo lectura de lo disponible para vender.

```
┌───────────────────────────────────────────────────────────────────────────┐
│  Stock de postres                                                           │
│  Postres disponibles para vender en « Sucursal Centro ».                    │
│                                                                             │
│  ┌── Card ───────────────────────────────────────────────────────────────┐ │
│  │ [ Buscar postre…                                                      ] │ │
│  │───────────────────────────────────────────────────────────────────────│ │
│  │ POSTRE                          DISPONIBLE                              │ │
│  │ Cheesecake                      8 u                                     │ │
│  │ Brownie                         6 u                                     │ │
│  │ Flan                            0 u          ← en muted (agotado)       │ │
│  └────────────────────────────────────────────────────────────────────────┘ │
└───────────────────────────────────────────────────────────────────────────┘
```
- Solo lectura, sin acciones (el descuento ocurre al vender en POS, F16). Ordenado por nombre.
- **DISPONIBLE**: `tabular-nums`. Filas en `0` en `text-muted` (agotado, no es error).
- Search opcional (útil si hay muchos postres); si tech lo omite, la tabla igual funciona.
- **Estados**: _vacío_ "Aún no tienes postres en stock. Aparecerán aquí cuando la repostería surta
  tus pedidos." · _loading_ "Cargando…" · _error_ inline danger.

---

## Pantalla 3 — Repostero: cola de producción (`/bakery/queue`)
Cubre F5, F6, F7–F11. Es el worklist del repostero (y del super admin en ausencia de repostero).

```
┌───────────────────────────────────────────────────────────────────────────────┐
│  Cola de producción                                                             │
│  Pedidos de postres de todas las sucursales. Los más antiguos primero.          │
│                                                                                 │
│  [ Por surtir ▾ ]  [ Todas las sucursales ▾ ]  [ Buscar postre… ]              │  ← filtros
│                                                                                 │
│  ┌── Card ─────────────────────────────────────────────────────────────────┐   │
│  │ ANTIG.   SUCURSAL   POSTRE      PEDIDO SURTIDO      FALTA ESTADO   ACCIÓN │   │
│  │ ⏱ 3 días  Norte     Cheesecake  10   ███░░ 3/10       7  ●En prod  [Producir]
│  │ 11/08     Centro    Flan        4    ░░░░░ 0/4        4  ●Pendiente [Producir]
│  │ hoy       Sur       Brownie     6    ░░░░░ 0/6        6  ●Pendiente [Producir]
│  │ 12/08     Centro    Galleta     12   ██████ 12/12     0  ●Surtido    —      │   │
│  └──────────────────────────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────────────────────────┘
```
- **Filtro Estado** (`Select`): default **"Por surtir"** (`pending` + `in_production`). Otras:
  Pendientes · En producción · Surtidos · Recibidos · Cancelados · Todos (F6).
- **Filtro Sucursal** (`Select`): "Todas" + sucursales. **Buscar postre** por nombre.
- **Orden**: más antiguo primero dentro de "por surtir" (FIFO → worklist).
- **FALTA** = `pedido − surtido`, en énfasis (`font-semibold`); es lo accionable (D2).
- **ANTIG.**: fecha del pedido; si `pending`/`in_production` y ≥2 días → **⏱ N días** en tono
  atención (D4).
- **[Producir]** visible solo si `pending`/`in_production` → abre el **Modal 3b**. En `shipped`/
  `received`/`cancelled` la acción es "—" (F11 CA: no se produce contra completos/cancelados).
- **Estados**: _vacío feliz_ (con filtro "Por surtir" y nada pendiente): "Cola al día. No hay
  pedidos por surtir." (no danger) · _vacío por filtro_: "Sin pedidos con estos filtros." ·
  _loading_ · _error_ inline danger.

### Pantalla 3b — Modal "Registrar producción" (F7–F11)
Se abre desde **[Producir]** de una fila. Contextualizado al pedido.

```
┌── Modal: Registrar producción ──────────────────────────────────────┐
│  Cheesecake  ·  para « Sucursal Norte »                        [ × ] │
│                                                                      │
│  Pedido: 10      Ya surtido: 3      Falta: 7                         │  ← contexto
│                                                                      │
│  Cantidad producida ahora  [ 7 ]                                     │  ← default = falta
│  Puede ser menor a lo que falta (producción parcial).               │  ← helper (F8)
│  ──────────────────────────────────────────────────────────────────│
│  Al registrar:                                                       │  ← preview doble efecto
│   • Consumirá del almacén central:                                   │    (F10)
│       Harina 700 g · Azúcar 350 g · Huevo 14 pza                     │
│   • Acreditará 7 u. de Cheesecake a « Sucursal Norte ».             │
│   • El pedido quedará « Surtido ».                                   │  ← o "En producción, faltarán N"
│                                                                      │
│  ⚠ El almacén no tiene suficiente Huevo (faltan 4 pza). Se           │  ← Alert warning condicional
│     registrará igual y el stock quedará en negativo.                 │    (D10, no bloquea)
│                                                                      │
│  [ Registrar producción ]   [ Cancelar ]                            │
└──────────────────────────────────────────────────────────────────────┘
```
- **Cantidad producida**: `number step=1 min=1`, default = `falta`. Se permite `> falta` (produjo de
  más); helper aclara la parcialidad (F8).
- **Preview en vivo** (D3): receta × cantidad (insumos que bajan) + stock que sube + estado
  resultante:
  - si `surtido + producido ≥ pedido` → "El pedido quedará **Surtido**".
  - si `<` → "El pedido quedará **En producción**, faltarán **{n}**".
  - receta vacía → "Este postre no tiene receta; no se descontarán insumos."
- **Aviso de insumos insuficientes** (`Alert warning`, condicional, D10): no bloquea (permite
  negativo, criterio warehouse). Si tech decide bloquear, se muestra como error y se deshabilita el
  botón.
- **[Registrar producción]** (`primary`, `loading`); al éxito → cierra modal, refresca la fila
  (progreso/estado) sin perder filtros. **Error** de API → inline dentro del modal.

---

## Pantalla 4 — Repostero: stock de insumos del almacén (`/bakery/supplies`)
Cubre F17. **Reuso** de la landing _Existencias del almacén_ (M8) en **solo lectura** (D5).

```
┌───────────────────────────────────────────────────────────────────────────┐
│  Insumos del almacén central                                               │
│  Solo lectura. Las compras y ajustes los gestiona el administrador.         │
│                                                                             │
│  ┌ ℹ 5 insumos en o por debajo de su mínimo. ─────────────────────────────┐│  ← Alert info
│  └──────────────────────────────────────────────────────────────────────── ┘│    (SIN botón comprar)
│  ┌── Card ───────────────────────────────────────────────────────────────┐ │
│  │ [ Buscar insumo… ]              [ Todas las categorías ▾ ]             │ │
│  │───────────────────────────────────────────────────────────────────────│ │
│  │ INSUMO        STOCK        MÍN       ESTADO                            │ │
│  │ Harina        12 000 g     4 000     ●OK                               │ │
│  │ Huevo         180 pza      200       ●Bajo mínimo                      │ │
│  │ Azúcar        0 g          1 000     ●Bajo mínimo                      │ │
│  └────────────────────────────────────────────────────────────────────────┘ │
└───────────────────────────────────────────────────────────────────────────┘
```
- **Diferencias vs. la landing de Almacén (M8):** se retira TODA la escritura — sin mín/máx
  editable, sin "Ajustar existencia", sin enlaces a Compras/Salidas/Mermas. Mín se muestra como
  **texto** (contexto para el badge), no como input.
- **Banner bajo-mínimo**: variante **info** y **sin** acción "Ver qué comprar" (D5): el repostero no
  compra; es solo señal de disponibilidad de material.
- Search + filtro de categoría reutilizados de `/warehouse` / `/supplies`.
- **Estados**: _loading_ · _vacío catálogo_ "Aún no hay insumos en el catálogo." · _error_ inline.

---

## Pantalla 5 — Tendencia de venta de postres (bloque reutilizable)
Cubre F18, F19. Bloque embebido en `/bakery/trend` (repostero) y en Insights (super_admin filtro /
branch_admin acotado). Compara **semana actual vs. anterior** por postre.

```
┌───────────────────────────────────────────────────────────────────────────┐
│  Tendencia de venta de postres                                             │
│  Sem. actual (07–13 ago) vs. anterior (31 jul–06 ago).  [ Todas ▾ ]        │  ← sucursal*
│                                                                             │
│  ┌── Card ───────────────────────────────────────────────────────────────┐ │
│  │ Más subieron: Cheesecake (+18 u), Brownie (+7 u).                      │ │  ← titular llano
│  │───────────────────────────────────────────────────────────────────────│ │
│  │ POSTRE        SEM. PASADA   ESTA SEM.     VARIACIÓN                    │ │
│  │ Cheesecake    24 u          42 u          ▲ +18 u  (+75%)   [ Subió ]  │ │  ← success
│  │ Brownie       15 u          22 u          ▲ +7 u   (+47%)   [ Subió ]  │ │
│  │ Croissant     10 u          10 u          =  0 u             [ Igual ] │ │  ← muted
│  │ Flan          18 u          11 u          ▼ −7 u   (−39%)   [ Bajó ]   │ │  ← danger
│  │ Pay de nuez   0 u           6 u           ▲ nuevo           [ Nuevo ]  │ │  ← accent
│  │ Galleta       9 u           0 u           ▼ −9 u   (−100%)  [ Bajó ]   │ │
│  └────────────────────────────────────────────────────────────────────────┘ │
│  Compara ventas de la semana en curso vs. la anterior. Histórico, sin       │  ← nota al pie (muted)
│  predicción.                                                                 │
└───────────────────────────────────────────────────────────────────────────┘
```
- **Filtro Sucursal (\*)**: visible para **repostero** y **super_admin** ("Todas" + sucursales);
  **branch_admin NO** lo ve (servidor lo acota a su sucursal, F19). "Todas" agrega por postre entre
  sucursales; elegir una sucursal muestra por postre de esa sucursal (D6).
- **Orden**: por **mayor variación en unidades** desc (los que más subieron arriba → qué reforzar).
- **VARIACIÓN**: ▲ (`success`) subió · ▼ (`danger`) bajó · `=` (`muted`) igual · **Nuevo**
  (`accent`) sin ventas la semana pasada · `−100%` = cayó a 0. Muestra Δ absoluto y % (el % se omite
  cuando la base es 0 → "nuevo").
- **Titular llano** (patrón _Tarjeta de insight_): resume los 1–2 que más subieron.
- **Estados**: _vacío_ "Sin ventas de postres en las últimas dos semanas." · _loading_ · _error_
  inline. (No aplica "muestra insuficiente": es conteo simple, no estadística.)

---

## Pantalla 6 — Ajustes de Sidebar (`components/Sidebar.tsx`)
Cubre F20/F21 (rol repostero) y los ítems nuevos de sucursal. Ver `handoff.md §1` para la tabla
completa por rol.

```
REPOSTERO (grupo plano, sin sección · landing /bakery/queue)      SUCURSAL (branch_admin)
┌───────────────────────────┐                                    ┌───────────────────────────┐
│ Faro.                      │                                    │ Faro.                     │
│                            │                                    │  Punto de venta            │
│  Cola de producción   ◀    │  ← activo (pastilla lime)          │  Gastos                    │
│  Insumos                   │                                    │  Clientes                  │
│  Tendencia de venta        │                                    │  Reportes                  │
│  ─────────────────────     │                                    │  Insights                  │
│  Mi cuenta                 │                                    │  Pedidos a repostería  ★   │  ← NUEVO
└───────────────────────────┘                                    │  Stock de postres      ★   │  ← NUEVO
                                                                  │  ──────────────────────    │
SUPER ADMIN (nuevo grupo)                                         │  Mi cuenta                 │
  … Administración / Operación / Insights / Clientes …            └───────────────────────────┘
  ┌ REPOSTERÍA ──────────────┐     cashier / barista: menú plano actual (Punto de venta, Gastos)
  │  Cola de producción      │       + Pedidos a repostería ★ + Stock de postres ★
  │  Stock de postres        │       (sin Tendencia — F19 excluye cashier/barista)
  └ ─────────────────────────┘
```
- **Repostero**: grupo plano (mismo estilo que roles de sucursal, sin encabezados de sección),
  landing directa `/bakery/queue`, sin selección de sucursal (F21). Iconos sugeridos: Cola →
  `ChefHat`; Insumos → `Boxes`; Tendencia → `TrendingUp`.
- **Super admin**: nuevo grupo **"Repostería"** (encabezado de sección muted): _Cola de producción_,
  _Stock de postres_. La tendencia vive dentro de su Insights.
- **branch_admin**: + _Pedidos a repostería_, + _Stock de postres_ (tendencia dentro de su Insights).
- **cashier / barista**: + _Pedidos a repostería_, + _Stock de postres_ (sin tendencia).
- Ítem activo = pastilla lime `bg-accent text-ink` (patrón actual, sin cambios).

---

## Pantalla 7 — Edición de producto: tipo + relabel de receta (`/products/[id]/edit`)
Cubre F1, F2. Solo **super_admin** ve el selector. Se añade un campo al form y se relabela la receta.

```
┌── Card: Editar producto ────────────────────────────────────────────┐
│  Nombre     [ Cheesecake                                          ]  │
│  Precio     [ 85.00 ]                                                │
│  Categoría  [ Postres                                          ▾ ]   │
│  ┌ ¿Cómo se surte? (solo super admin) ──────────────────────────┐   │  ← NUEVO (F1)
│  │  ( ) Preparado en sucursal                                    │   │
│  │  (•) De repostería central                                    │   │
│  │  El postre se produce en la central y se surte a las          │   │  ← helper del tipo elegido
│  │  sucursales por pedido; su receta se consume al producir.     │   │
│  └───────────────────────────────────────────────────────────────┘   │
│  Imagen     [ ↑ subir ]                                              │
│  [ Guardar ]  [ Cancelar ]                                           │
└──────────────────────────────────────────────────────────────────────┘
┌── Card: Receta ─────────────────────────────────────────────────────┐
│  Insumos que consume producir una unidad de este postre en la        │  ← subtítulo RELABEL (F2)
│  repostería central. Se descuentan del almacén central al registrar   │    cuando es "de repostería"
│  la producción (no al vender).                                        │
│  … (mismo editor de receta, sin cambios estructurales) …             │
└──────────────────────────────────────────────────────────────────────┘
```
- **Selector "¿Cómo se surte?"** (radio o `Select`): _Preparado en sucursal_ (`branch_prepared`,
  **default**) · _De repostería central_ (`bakery`). Solo visible/editable por super_admin (F1).
  También en `/products/new` (nace `branch_prepared`).
- **Helper contextual** bajo el selector explica la consecuencia del tipo elegido.
- **Relabel de la Receta** (D7): mismo editor; solo cambia el subtítulo:
  - `branch_prepared` → "Insumos que consume una unidad de este producto. Se descuentan del stock al
    **vender**." (texto actual).
  - `bakery` → "Insumos que consume **producir** una unidad de este postre en la repostería central.
    Se descuentan del almacén central al registrar la producción (no al vender)."
- **Precaución (nota para tech):** cambiar el tipo de un producto con pedidos abiertos o stock
  acreditado puede dejar datos inconsistentes; la UI puede mostrar un aviso, pero la regla la fija
  tech-spec (fuera del alcance de este diseño).

---

## Componentes reutilizados / nuevos
- **Reutilizados:** `Card`, `Button` (primary/outline/ghost), `Input`, `Select`, `SearchableSelect`,
  `DateInput`, `FormField`, `Alert` (info/warning), `StatusBadge`, `Modal`, patrón _tabla de
  historial_ (M8), patrón _Ranking list con barra_ / _Comparación por segmentos_ (M9),
  search + filtro de categoría (`/warehouse`).
- **Nuevos / a formalizar en design-system (§M10):**
  1. **Progreso de surtido** — `n/m` + barra proporcional para producción parcial (D2).
  2. **Indicador de antigüedad (aging)** — reloj + "N días" en tono atención para worklists (D4).
  3. **StatusBadge — variante `success`** (verde) para estados de completado (surtido/recibido) (D8).
  4. **Fila de variación semana vs. semana** — ▲/▼/=/nuevo con Δ y % (reuso de _Comparación por
     segmentos_ aplicada a serie temporal) (D6).
  Detalle en `handoff.md §4` y en `foundations/design-system.md`.
</content>
