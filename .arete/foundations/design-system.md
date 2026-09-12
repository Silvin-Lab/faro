# Design System — Faro
_Versión: 0.3 · Fecha: 2026-06-29 · Nace con: módulo login (M1) · Crece con cada módulo_

> **Referencia visual:** _BrightPOS — Point of Sale Dashboard UI_ (Dribbble shot 27026796).
> Identidad: **acento lime/verde-limón** sobre **fondo claro**, sidebar **blanco** con navegación agrupada, ítem activo como **pastilla lime con texto oscuro**, tipografía sans limpia. Estética luminosa y amigable, pensada para mostrador.
>
> Valores de color aproximados a partir de la imagen de referencia; afinar con el archivo original si se requiere precisión exacta.

## Tokens
### Color
- `--color-accent` (lime, **primario/acción**): `#C4E456`
- `--color-accent-strong` (hover/activo): `#B2D63F`
- `--color-on-accent` (texto/iconos sobre lime): `#1A1A1A`  ← **texto oscuro sobre el lime, no blanco**
- `--color-bg` (fondo de página): `#F4F4F2`
- `--color-surface` (sidebar, cards, top bar): `#FFFFFF`
- `--color-text`: `#1A1A1A` · `--color-text-muted`: `#8C8C8C`
- `--color-border`: `#E6E6E4`
- `--color-danger`: `#C0392B` · `--color-success`: `#2E7D32`

### Tipografía
- Familia base UI: **Inter** (excelente para precios/números del POS).
- Marca/encabezados pueden usar una sans redondeada (**Poppins**) para el toque amigable de la referencia.
- Escala (px): `12 · 14 · 16 · 20 · 24 · 32`. Pesos: 400 / 500 / 600 (logo en 700).

### Espaciado / radios / sombras
- Espaciado (px): `4 · 8 · 12 · 16 · 24 · 32`.
- Radios: `sm 8 · md 12 · lg 16` (la referencia usa esquinas bien redondeadas en pastillas y cards).
- Sombra: `sm` muy sutil en cards; el layout se apoya más en bordes claros que en sombras.

## Accesibilidad (reglas base)
- Contraste mínimo **AA**. ⚠️ El lime es claro: **texto sobre lime siempre oscuro** (`--color-on-accent`), nunca blanco.
- Foco visible en todos los controles. Áreas táctiles ≥ 44×44 px (tablet de mostrador).
- Inputs siempre con `<label>` asociado.

## Layout — App shell (cimiento, estilo BrightPOS)
- **Top bar** (blanco): logo **Faro** (izq) + acciones globales (búsqueda, notificaciones, menú "…").
- **Sidebar izquierdo** (blanco, fijo): navegación **agrupada por secciones** con encabezados en gris muted (ej. "Main Menu", "Support"). Cada ítem = icono outline + label. **Ítem activo = pastilla lime (`--color-accent`) con texto oscuro y radio `lg`.**
- **Área de contenido**: breadcrumb (ej. "Dashboard › New Transaction") + título de página + acciones a la derecha (ej. botón "Back" con borde y icono lime).
- Responsive: sidebar colapsable en pantallas chicas. Este shell lo reusan TODOS los módulos.

## Componentes (los que usa login)
### Button — variantes y estados
- `primary`: **fondo lime + texto oscuro**; hover → `accent-strong`. Estados: default / hover / active / disabled / loading.
- `outline`: fondo blanco, borde claro, **icono/acento lime** (como "Back"/"Notification" de la referencia).
- `ghost`: sin fondo, para acciones secundarias (ej. "Salir").
### Input (text / email / password) — estados: default / focus / error / disabled
- Con label, mensaje de error; password con toggle de visibilidad.
### Form field — label + input + texto de error.
### Card / Surface — blanco, borde claro, radio `md`.
### Sidebar nav item — estados: default / hover / **activo (pastilla lime, texto oscuro)** / disabled.
### Section header (sidebar) — texto muted, mayúscula/espaciado, separa grupos ("Main Menu", "Support").

## Patrones
- **Acción principal = lime con texto oscuro.** Acentos (activos, "+", chips seleccionados) en lime.
- **Formulario:** label arriba, error debajo, acción principal a ancho completo en móvil/tablet.
- **Feedback:** errores como inline/toast; mensajes genéricos en credenciales.

## Extensiones — M8 Almacén (warehouse)
_Añadido: 2026-07-20 (product-designer). Patrones nacidos al diseñar el módulo Almacén; reutilizables por otros módulos._
- **Select field** — mismo look que `Input`: `w-full rounded-md border border-line bg-surface px-3 py-2 text-sm text-ink outline-none focus:border-accent-strong`. Hoy vive como clase repetida (`selectClass`) en `supplies`; **formalizar como `components/ui/Select.tsx`**. Estados: default / focus / disabled. Siempre con `<label>` (via Form field).
- **Date input** — `type=date` con los tokens de `Input`. Display de fechas `dd/mm/aaaa` (o con hora donde aplique).
- **Alert / Callout banner** — banda a ancho completo dentro del área de contenido: icono + texto + acción (enlace). Radio `md`, borde claro. Variantes: **info** (fondo `--color-bg`) y **warning/atención** (acento sutil). Uso: alerta de reposición ("N insumos bajo su mínimo → Ver qué comprar"). Se muestra condicionalmente (oculto si no hay nada que alertar).
- **Status badge** — pastilla `rounded px-2 py-0.5 text-xs`. Variantes: **OK/activo** (`bg-accent text-ink`), **atención/negativo** (`--color-danger`, tinte suave), **neutro/sin dato** (`bg-bg text-muted`). Generaliza el badge activo/inactivo ya usado en `supplies`.
- **Sidebar section group** — primer uso real del Section header ya descrito: encabezado de sección muted (ej. "Almacén") + sus items agrupados debajo. El sidebar deja de ser una lista plana única y admite varios grupos.
- **Tabla de historial** — patrón ya usado en `supplies/[id]/edit`, formalizado: contenedor `overflow-x-auto` + `min-w-[…]`; `thead` `border-b border-line text-xs uppercase tracking-wide text-muted`; `tbody` `divide-y divide-line`; números `tabular-nums`; cantidades negativas en `--color-danger`. Estado vacío: texto muted ("Sin … registrados.").

## Extensiones — M9 Insights (insights)
_Añadido: 2026-07-27 (product-designer). Patrones nacidos al diseñar el módulo Insights (inteligencia de negocio descriptiva, sin IA). Reutilizables por otros módulos analíticos._
- **Filtro rango + sucursal reutilizable** — el selector de `internal/reports` (pills de rango + `Input type=date` "Desde/Hasta" + "Aplicar" + `Select` de sucursal solo para super admin) se **formaliza como componente compartido** con **presets configurables** y misma semántica de scope (`[from,to)` + `tz` + `branchId`). Reportes usa presets `Hoy / Ayer / Personalizado` (default Hoy, con auto-refresh); Insights usa `Últimos 30 días / Últimos 90 días / Personalizado` (default 30 días, **sin** auto-refresh). El componente es el mismo; el contenido de presets y el auto-refresh son props del contexto.
- **Tarjeta de insight** — `Card` con estructura fija: **titular en lenguaje llano** (la lectura de negocio, `text-lg/xl font-semibold`) → detalle de respaldo (conteos, barras, señal de muestra) → **estados por-tarjeta**. Regla del módulo: nunca un número suelto; siempre trae numerador/denominador, conteo de respaldo o señal de muestra. No es un chart abstracto: la visualización es de apoyo, subordinada al titular.
- **Stat / KPI block** — label muted (`text-sm text-muted`) + número grande `tabular-nums` (`text-3xl/4xl font-bold`) + sub-línea de respaldo opcional ("N ventas", "k/N"). Generaliza el "Total vendido / Ventas" de Reportes. Se agrupa en `grid sm:grid-cols-2|3` para segmentos comparables.
- **Ranking list con barra** — fila `rank · label · valor` + barra proporcional al máximo de su lista (`bg-accent` sobre `bg-bg`, radio `sm`), `tabular-nums`. Generaliza el desglose por categoría de Reportes. Varios rankings lado a lado (`grid sm:grid-cols-3`) cuando el **contraste entre criterios es el mensaje** (p. ej. ingresos vs. volumen vs. margen).
- **Comparación por segmentos** — 2–3 columnas de stat con una **fila de contraste** (▲/▼ % o `×`) que hace legible la diferencia de un golpe (canjeó vs. no canjeó; nuevo vs. recurrente vs. anónimas). El contraste, no el valor absoluto, es lo accionable.
- **Estado "muestra insuficiente"** — variante de estado vacío **distinta** de "sin datos": "vacío" = no hubo datos en el rango; "muestra insuficiente" = hubo datos pero muy pocos para concluir. Copy propio ("Muestra insuficiente: solo N … amplía el rango.") en vez de mostrar un número espurio. Evita accionar sobre ruido en insights estadísticos (afinidad, visita-N).
- **Nota al pie de contexto (muted)** — línea `text-xs text-muted` bajo el detalle de una tarjeta para exclusiones estructurales o aclaraciones ("No incluye ventas anónimas …"). Es **información, no una alerta** (no usa el `Alert`/danger): distingue "no aplica por el modelo de datos" de "algo salió mal".

## Extensiones — M10 Repostería / Producción central (bakery)
_Añadido: 2026-08-13 (product-designer). Patrones nacidos al diseñar el módulo de Repostería (pedido de sucursal → producción → acreditación de stock). Reutilizables por otros módulos con flujos de "avance vs. objetivo" y worklists._
- **Progreso de surtido (`n/m` + barra)** — celda que muestra `despachado/pedido` (`tabular-nums`) + barra proporcional (`bg-accent` sobre `bg-bg`, radio `sm`). Es la _Ranking list con barra_ (M9) reaplicada a "avance contra un objetivo" (producción parcial). Estado 0 = barra vacía. Siempre con `aria-label` descriptivo ("3 de 10 surtidos"). Acompañarla de una columna **"Falta"** en énfasis (`font-semibold`) cuando lo accionable es lo que resta.
- **Indicador de antigüedad (aging)** — icono reloj + "N días" en **tono atención** (`text-danger` / tinte suave), **no** un `Alert`: señala prioridad en una cola/worklist sin connotar error. Umbral configurable (default 2 días). Uso: pedidos `pending`/`in_production` sin cerrar.
- **Status badge — variante `success`** — se añade a las variantes de M8: **success** (`bg-success/10 text-success`, verde) para estados de **completado**. Con esto el `StatusBadge` cubre ciclos de estado más largos. Mapeo de referencia (pedido de repostería): `pending→muted · in_production→accent · shipped→success · received→success(+✓) · cancelled→danger`.
- **Fila de variación temporal (periodo vs. periodo)** — ▲ (`success`, subió) / ▼ (`danger`, bajó) / `=` (`muted`, igual) / **"nuevo"** (`accent`, sin base previa), con Δ absoluto y % (el % se omite si la base es 0). Es la _Comparación por segmentos_ (M9) aplicada a una **serie temporal** (semana actual vs. anterior). Regla: ordenar por el criterio accionable (p. ej. Δ en unidades, no alfabético) y anteponer un **titular llano** con los mayores movimientos.

## Versionado
- Estable: paleta BrightPOS (lime), tipografía (Inter/Poppins), tokens, Button (primary/outline/ghost), Input, Form field, Card, App shell (top bar + sidebar agrupado), Sidebar nav item, Section header. **+M8:** Select field, Date input, Alert/Callout banner, Status badge (OK/atención/neutro), Sidebar section group, Tabla de historial. **+M9:** Filtro rango+sucursal reutilizable (presets configurables), Tarjeta de insight, Stat/KPI block, Ranking list con barra, Comparación por segmentos, Estado "muestra insuficiente", Nota al pie de contexto. **+M10:** Progreso de surtido (n/m + barra), Indicador de antigüedad (aging), Status badge variante `success`, Fila de variación temporal (periodo vs. periodo).
- Próximas extensiones (otros módulos): chips de categoría (estilo tabs lime), stepper de cantidad ("− n +" con + lime), grid de cards de producto, breadcrumb.
