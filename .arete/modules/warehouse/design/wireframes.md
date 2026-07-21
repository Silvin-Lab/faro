# Wireframes — Almacén (warehouse)
_Autor: product-designer · Fecha: 2026-07-20 · Módulo: M8 · Fuente: prd.md (aprobado) · research.md_

Wireframes de baja fidelidad (estructura, jerarquía y estados). El look final se hereda del design
system de Faro (BrightPOS lime) y de los componentes existentes (`Card`, `Button`, `Input`,
`FormField`, tablas y listas de `supplies`). Las specs de estados/comportamiento y tokens van en
`handoff.md`.

## Mapa de navegación (sección Almacén)

Nueva sección **Almacén** en el sidebar (primer grupo con encabezado de sección, estilo del DS).
Rutas propuestas (definitivas las decide tech, pero el diseño asume estas):

```
ALMACÉN                         (section header, muted)
├─ Almacén            /warehouse                → Existencias del almacén (landing) + mín/máx
├─ Productos a comprar /warehouse/to-buy        → insumos en/bajo mínimo   [DECISIÓN D1]
├─ Compras           /warehouse/purchases       → alta de compra + historial
├─ Salidas           /warehouse/dispatches      → alta de salida + historial
├─ Mermas            /warehouse/waste           → alta de merma + historial
└─ Proveedores       /warehouse/suppliers       → CRUD proveedores
```

Flujo principal (reposición):
```
Almacén (landing) --banner--> Productos a comprar --"Registrar compra"--> Compras (prefilled)
```

---

## Pantalla 1 — Almacén / Existencias del almacén  (landing, `/warehouse`)
Cubre F1, F2 (mín/máx inline), F3 (stock por insumo), y entrada al flujo F4.

```
┌───────────────────────────────────────────────────────────────────────┐
│  Almacén                                                                │
│  Existencias del almacén central.                                       │
│                                                                         │
│  ┌───────────────────────────────────────────────────────────────┐    │  ← banner reposición
│  │ ⚠  5 insumos en o por debajo de su mínimo.   [Ver qué comprar →]│    │    (solo si N>0)
│  └───────────────────────────────────────────────────────────────┘    │
│                                                                         │
│  ┌── Card ─────────────────────────────────────────────────────────┐   │
│  │ [ Buscar insumo…            ]   [ Todas las categorías ▾ ]       │   │
│  │─────────────────────────────────────────────────────────────────│   │
│  │ INSUMO        STOCK       MÍN     MÁX     ESTADO                  │   │  ← thead muted
│  │ Leche entera  12 000 ml   [4000]  [20000] ● OK                    │   │
│  │ Café grano    800 g       [1000]  [ 8000] ● Bajo mínimo           │   │  ← badge danger
│  │ Vasos 12oz    0 pieza     [ 200]  [ 2000] ● Bajo mínimo           │   │
│  │ …                                                                │   │
│  └─────────────────────────────────────────────────────────────────┘   │
└───────────────────────────────────────────────────────────────────────┘
```
- **Mín/máx editables inline** (inputs numéricos por fila; guardan al blur/enter). Decisión D2.
- **Estado** = badge derivado: `stock ≤ mín` → "Bajo mínimo" (danger); si no → "OK" (accent).
- Reutiliza search + filtro de categoría de `/supplies`.
- Estados: **loading** ("Cargando…"), **vacío** ("Aún no hay insumos en el catálogo. Créalos en
  Insumos."), **error** (texto danger). Banner **oculto** cuando N=0.

---

## Pantalla 2 — Productos a comprar  (`/warehouse/to-buy`) — DECISIÓN D1
Cubre F4. Lista TODOS los insumos con `stock ≤ mínimo`.

```
┌───────────────────────────────────────────────────────────────────────┐
│  Productos a comprar                                                    │
│  Insumos en o por debajo de su mínimo de almacén.                       │
│                                                                         │
│  ┌── Card ─────────────────────────────────────────────────────────┐   │
│  │ INSUMO      STOCK     MÍN     FALTAN     PRESENTACIÓN      ACCIÓN │   │
│  │ Café grano  800 g     1000 g  200 g      Bolsa 1000 g   [Comprar]│   │
│  │ Vasos 12oz  0 pieza   200     200        Paquete 50 pz  [Comprar]│   │
│  └─────────────────────────────────────────────────────────────────┘   │
└───────────────────────────────────────────────────────────────────────┘
```
- **FALTAN** = `mín − stock` (nunca negativo) en unidad base; ayuda al encargado a dimensionar.
- **PRESENTACIÓN** = `packageName` del insumo (contexto de compra, F5).
- **[Comprar]** → `/warehouse/purchases?supplyId=…` (formulario de compra con insumo preseleccionado).
- Estado **vacío (feliz)**: "Todo en orden. Ningún insumo está por debajo de su mínimo." (no danger).
- Estados loading / error estándar.

---

## Pantalla 3 — Compras  (`/warehouse/purchases`)
Cubre F5, F7. Formulario (card superior) + historial (card inferior). Decisión D5.

```
┌── Card: Registrar compra ───────────────────────────────────────────┐
│  Fecha            [ 2026-07-20            ] (date)                    │
│  Proveedor        [ Selecciona…        ▾ ]   [+ Nuevo proveedor]     │
│  Insumo           [ Selecciona…        ▾ ]                            │
│  Presentación     Bolsa 1000 g · +1000 g al almacén por presentación │  ← info derivada
│  Precio de compra [ 0.00 ] pesos  (por presentación)                 │
│  Cantidad         [ 0 ] presentaciones                               │
│  ─────────────────────────────────────────────────────────────────  │
│  Entran 3000 g al almacén · Total $255.00                            │  ← cálculo en vivo
│  [ Registrar compra ]                                                 │
└──────────────────────────────────────────────────────────────────────┘
┌── Card: Historial de compras ───────────────────────────────────────┐
│ FECHA       INSUMO      PROVEEDOR    CANT.   PRECIO/PRES  TOTAL  QUIÉN│
│ 20/07/2026  Café grano  Distrib. X   3       $85.00    $255.00  Ana  │
│ …                                                                    │
└──────────────────────────────────────────────────────────────────────┘
```
- Precio **por presentación** (no por unidad base) — F5. Cálculo en vivo:
  `unidades base = cantidad × packageContent`; `total = cantidad × precio`.
- Prefill de `supplyId` desde "Productos a comprar".
- Botón deshabilitado hasta: proveedor + insumo + cantidad>0.
- Estados: submit **loading**, **error** inline; historial **vacío** ("Sin compras registradas.").

---

## Pantalla 4 — Proveedores  (`/warehouse/suppliers`) + alta/edición
Cubre F6. CRUD nuevo (patrón lista + `/new` + `/[id]/edit`, como Categorías de insumo).

Lista:
```
┌───────────────────────────────────────────────────────────────────────┐
│  Proveedores                                   [ Nuevo proveedor ]      │
│  Para seleccionarlos al registrar una compra.                           │
│  ┌── Card ─────────────────────────────────────────────────────────┐   │
│  │ Distribuidora X   ·  55 1234 5678 · ventas@distx.com  [Editar]   │   │
│  │ Café del Valle    ·  55 2222 1111 · —                 [Editar]   │   │
│  └─────────────────────────────────────────────────────────────────┘   │
└───────────────────────────────────────────────────────────────────────┘
```
Alta / edición (`Card max-w-lg`):
```
┌── Nuevo proveedor ──────────────────────────────────────────────────┐
│  Nombre*          [                    ]                              │
│  Teléfono         [                    ]                              │
│  Correo           [                    ] (email)                      │
│  Dirección        [                    ]                              │
│  [ Guardar ]   [ Cancelar ]                                          │
└──────────────────────────────────────────────────────────────────────┘
```
- Solo **Nombre** obligatorio; dirección/correo/teléfono opcionales (dato de contacto).
- Correo con `type=email` (validación de formato). Estados new/edit: loading, error inline.
- Lista: **vacío** ("Aún no hay proveedores."), loading, error.

---

## Pantalla 5 — Salidas  (`/warehouse/dispatches`)
Cubre F8, F9, F10. Formulario + historial. Decisión D5.

```
┌── Card: Registrar salida ───────────────────────────────────────────┐
│  Fecha            [ 2026-07-20        ] (date)                        │
│  Insumo           [ Selecciona…    ▾ ]                                │
│  Cantidad         [ 0 ]  g            (unidad base del insumo)        │
│  Sucursal destino [ Selecciona…    ▾ ]                                │
│  ─────────────────────────────────────────────────────────────────  │
│  Resta 500 g del almacén y suma 500 g a "Sucursal Centro".           │  ← en vivo
│  [ Registrar salida ]                                                 │
└──────────────────────────────────────────────────────────────────────┘
┌── Card: Historial de salidas ───────────────────────────────────────┐
│ FECHA       INSUMO      CANTIDAD   SUCURSAL DESTINO   QUIÉN           │
│ 20/07/2026  Leche       -500 ml    Sucursal Centro    Ana            │
└──────────────────────────────────────────────────────────────────────┘
```
- Sucursal destino **obligatoria** (a diferencia de Mermas).
- Texto de ayuda deja explícito el reflejo automático en la sucursal (F9/F10) → refuerza confianza.
- Estados estándar; historial vacío ("Sin salidas registradas.").

---

## Pantalla 6 — Mermas  (`/warehouse/waste`) — DECISIÓN D3
Cubre F11, F12. Un solo formulario, **sucursal opcional**.

```
┌── Card: Registrar merma ────────────────────────────────────────────┐
│  Producto eliminado, caducado o desechado. Si salió a una sucursal   │  ← helper (2 casos)
│  y allí se desechó, elige esa sucursal; si se perdió en el almacén,  │
│  deja "Sin sucursal".                                                │
│                                                                      │
│  Fecha            [ 2026-07-20        ] (date)                        │
│  Insumo           [ Selecciona…    ▾ ]                                │
│  Cantidad         [ 0 ]  g                                            │
│  Sucursal         [ Sin sucursal (almacén central) ▾ ]   (opcional)  │
│  Motivo           [ Ej. caducado, dañado                    ]         │
│  [ Registrar merma ]                                                  │
└──────────────────────────────────────────────────────────────────────┘
┌── Card: Historial de mermas ────────────────────────────────────────┐
│ FECHA       INSUMO   CANTIDAD   SUCURSAL        MOTIVO      QUIÉN     │
│ 20/07/2026  Galleta  -10 pieza  Sucursal Sur    no vendido  Ana      │
│ 20/07/2026  Leche    -2000 ml   —               caducado    Ana      │
└──────────────────────────────────────────────────────────────────────┘
```
- **Sucursal** default = "Sin sucursal (almacén central)" (caso a). Elegir sucursal = caso b.
- **Motivo** obligatorio (auditoría, F12). Cantidad>0 obligatoria.
- Historial: sucursal vacía se muestra como "—".

---

## Pantalla 7 — Limpieza de `/supplies/[id]/edit`  (F14)  — [ACTUALIZADO 2026-07-20: resolución N1]
Retirar **solo los 2 formularios de escritura**; **conservar el historial de solo lectura**.

```
ANTES                                  DESPUÉS
┌ Datos ────────────┐                  ┌ Datos ────────────┐
├ Medidas de uso ───┤                  ├ Medidas de uso ───┤
├ Entrada de compra ┤  ← QUITAR (form) ├ Historial mov. ───┤  ← SE QUEDA (solo lectura)
├ Ajuste / merma ───┤  ← QUITAR (form) └───────────────────┘
└ Historial mov. ───┘  ← SE QUEDA      + nota: "El registro de existencias
                                          (compras, salidas, mermas) se
                                          gestiona ahora en Almacén."
```
- Quedan **Datos**, **Medidas de uso** y **Historial de movimientos** (solo lectura).
- Se eliminan únicamente los **formularios de alta** de "Entrada de compra" y "Ajuste / merma"
  (con su `<select>` de sucursal y estados de escritura). El historial mantiene su tabla y ahora
  etiqueta también `transfer` ("Entrada de almacén") y `waste` ("Merma").
- Añadir una línea de ayuda con enlace a la sección Almacén, para no dejar al usuario buscando las
  funciones que se movieron.

---

## Componentes reutilizados / nuevos
- **Reutilizados:** `Card`, `Button` (primary/outline/ghost), `Input`, `FormField`, patrón de
  tabla (`supplies/[id]/edit` historial), patrón de lista (`supplies`), search+filtro de categoría,
  badges de estado (`bg-accent`/`bg-bg`).
- **Nuevos (formalizar en design-system):** _Select field_ (hoy es `selectClass` repetido inline),
  _Alert/Callout banner_ (reposición), _grupo de sección en sidebar_ (encabezado + items),
  _date input_. Detalle en `handoff.md` y en `foundations/design-system.md`.
</content>
