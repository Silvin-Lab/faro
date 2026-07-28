# Research / Decisiones de UX — Insights (insights)
_Autor: product-designer · Fecha: 2026-07-27 · Módulo: M9 · Fuente: prd.md (aprobado, Silvin 2026-07-27)_

> No hubo research primario nuevo. El único consumidor está identificado y alineado
> (Silvin, dueño del negocio; y el encargado de sucursal como lector acotado). El PRD ya cerró
> las 6 preguntas de producto (P1–P6). Este documento captura los **jobs-to-be-done** y las
> **decisiones de UX** que gobiernan los wireframes y el handoff, para que ingeniería entienda el
> _por qué_. Todo lo demás (definiciones, exclusiones, umbrales) ya está fijado en el PRD.

## Contexto de partida (lo que ya existe y se reutiliza)
- **Reportes** (`app/(app)/reports/page.tsx`): página única, scroll largo de `Card`s, con un
  **selector de rango + sucursal** arriba (pills `Hoy / Ayer / Personalizado`, `Input type=date`,
  select de sucursal solo para super admin) y **carga por sección con error aislado** (los gastos
  fallan sin romper las ventas). Es el patrón que Insights debe heredar.
- **Design system Faro v0.3 (+M8)**: `Card`, `Button`, `Input`, `Select`, `DateInput`,
  `StatusBadge`, `Alert`, `DistributionChart` (barras/pie con paleta CVD-safe), tabla de historial,
  sidebar agrupado por secciones. Tokens: `text-ink`, `text-muted`, `text-danger`, `bg-accent`,
  `bg-accent-strong`, `bg-surface`, `bg-bg`, `border-line`. **No se define un DS nuevo.**
- **Sidebar** (`components/Sidebar.tsx`): para super admin ya está **agrupado** (Administración /
  Operación / Clientes / Almacén); Reportes vive en **Operación**. Branch admin y cajero tienen menú
  plano. El patrón de "sección nueva" ya está resuelto técnicamente.

## Jobs-to-be-done
1. **"Cuando pienso mi mes, quiero saber de un vistazo cuántos clientes vuelven y qué tan rápido,
   para saber si estoy reteniendo o solo vendiendo una vez."** → Insight 1 (recurrencia + cohorte
   30/60/90), con lectura en lenguaje simple ("X de cada Y vuelven"), no un gráfico abstracto.
2. **"Quiero saber cuál es mi producto estrella, pero según lo que importa: ¿el que más factura, el
   que más se vende, o el que más margen deja?"** → Insight 2 (3 rankings lado a lado para ver el
   contraste, que es lo accionable).
3. **"¿El cliente que vuelve gasta más que el nuevo? ¿Y cuánto pesa la venta anónima de mostrador?"**
   → Insight 3 (ticket promedio en 3 segmentos, cada uno con su volumen de respaldo).
4. **"¿Qué se pide en la segunda visita? Quiero un gancho para que el cliente nuevo vuelva."**
   → Insight 4 (productos sobre-representados en la visita #2, con señal de muestra para no accionar
   sobre ruido).
5. **"¿Qué se compra junto? Quiero armar combos o reubicar el menú."** → Insight 5 (top de pares con
   fuerza de asociación y soporte).
6. **"¿La lealtad de verdad mueve la aguja o solo regala café?"** → Insight 6 (canjeó vs. no canjeó:
   ticket, recurrencia, gasto).

## Principio rector del módulo: "tarjeta de insight", no dashboard de gráficas
El PRD es explícito: cada insight es una **unidad legible y accionable**, no un chart genérico.
Traducción a UX (gobierna todos los wireframes):
- **Titular en lenguaje llano primero.** Cada tarjeta abre con la lectura de negocio ("3 de cada 10
  clientes vuelven", "Tu producto estrella por margen es X"), no con el eje de un gráfico.
- **El número trae su contexto.** Nunca un % suelto: siempre numerador/denominador, conteo de
  respaldo, o señal de muestra (lo exige el PRD en F1, F10, criterios de Insight 4/5).
- **La visualización es de apoyo, subordinada al titular.** Barras proporcionales (ya usadas en
  Reportes) para rankings; nada de ejes cartesianos abstractos. Se reutiliza el lenguaje visual de
  Reportes (barra lime sobre `bg-bg`, chips de %).
- **El contraste es el mensaje.** En Insight 2 (ingresos vs. volumen vs. margen) y en Insight 6
  (canjeó vs. no) lo accionable es la diferencia entre columnas; el layout las pone lado a lado.

## Decisiones de UX (con rationale)

### D1 — Insights es **una página** con un selector compartido, no 6 sub-páginas
El PRD pide "sección nueva Insights" y "mismo rango + sucursal para todos los insights".
Decisión: **una sola ruta `/insights`** con el selector arriba (rango + sucursal) y las **6 tarjetas
apiladas** debajo, exactamente como Reportes resuelve sus múltiples secciones en una página.
- Rationale: los 6 insights comparten filtro; separar en 6 páginas obligaría a re-seleccionar rango
  y sucursal 6 veces y rompería la lectura comparada. Un solo scroll con filtro compartido es lo más
  simple (principio de simplicidad) y lo más consistente con Reportes.
- **Sidebar:** entrada propia **"Insights"**, separada de "Reportes" (el PRD lo pide explícitamente:
  es comportamiento, no cifras operativas del día). Para **super admin** se agrega como **sección
  propia** en el sidebar agrupado (label "Insights", un ítem); para **branch admin** como ítem del
  menú plano, junto a Reportes. Ver handoff §1. Icono sugerido: `Lightbulb` (evitar `Sparkles`: el
  PRD descarta cualquier connotación de IA).

### D2 — Presets del selector adaptados a comportamiento (default 30 días), reusando el patrón visual de Reportes
Reportes por defecto abre en **"Hoy"** porque mide la operación del día. Los insights de
comportamiento (recurrencia, cohortes 30/60/90, afinidad) **carecen de sentido en una ventana de un
día** — darían tarjetas casi vacías al abrir.
- Decisión: se **reutiliza el mismo componente/estética** del selector de Reportes (pills de rango +
  `Input type=date` "Desde/Hasta" + "Aplicar" + select de sucursal para super admin), pero los
  **presets se ajustan al dominio**: `Últimos 30 días` (default) · `Últimos 90 días` ·
  `Personalizado`. La semántica `[from, to)` + `tz` + `branchId` es idéntica a `internal/reports`
  (lo exige el PRD).
- Rationale: "mismo selector" se honra como **mismo patrón y misma semántica de scope**, no como
  "mismos botones de rango". Cambiar los presets es una adaptación de contenido, no un componente
  nuevo. Se documenta explícitamente para que tech no copie los presets `Hoy/Ayer` sin pensar.
- **Sin auto-refresh de 60 s ni anillo** (`RefreshRing`): Reportes lo tiene porque es un tablero
  operativo en vivo; Insights es análisis reflexivo sobre rangos largos. Se omite (menos ruido,
  menos carga). Recarga solo al cambiar filtro / "Aplicar".

### D3 — Cada tarjeta gestiona sus **propios estados** de forma aislada
Como Reportes carga los gastos con su propio error sin romper las ventas, **cada insight** resuelve
por separado: **carga · vacío · muestra insuficiente · error**. Un insight que falle (o que no tenga
datos) no debe tumbar el resto de la página.
- Rationale: los 6 insights son queries independientes de peso muy distinto (afinidad y cohortes son
  las candidatas a pesar, T2 del PRD). Aislar estados permite además que tech decida por-insight si
  se calcula en vivo o se pre-computa, sin cambiar la UI. El PRD exige estado vacío / de muestra
  insuficiente en varios insights (1, 4, 5, 6) — aquí se sistematiza para los 6.

### D4 — "Ventas anónimas" es un **elemento visible de contexto**, con tratamiento diferenciado por insight (según PRD P1)
El PRD fija el trato por insight; la UX lo materializa así:
- **Insights 1 y 3:** bucket anónimo como **tercer segmento explícito** (chip/columna propia), nunca
  mezclado con nuevo/recurrente. En Insight 1 va como una línea de contexto muted ("X% de tus ventas
  son anónimas — N ventas"); en Insight 3 como la **tercera columna** de ticket promedio.
- **Insights 4 y 6:** exclusión **estructural** (sin `customer_id` no hay a quién atribuir). Se
  comunica con una **nota al pie muted**, no con un estado de error: "No incluye ventas anónimas
  (sin cliente no se puede secuenciar visitas / atribuir canjes)". Es información, no una alerta.
- **Insights 2 y 5:** las anónimas cuentan con normalidad; no requieren tratamiento visible especial.
- Rationale: el PRD prohíbe expresamente excluir en silencio (1/3) y a la vez pide no fingir que
  4/6 pueden incluirlas. La distinción visual (segmento propio vs. nota al pie) evita que Silvin
  interprete "no está" como "no hay datos".

### D5 — "Sin costo capturado" se muestra, no se esconde (Insight 2 / F7)
Un producto excluido del ranking de margen (insumo con `packageCostCents` nulo, o sin receta) **no
puede desaparecer sin explicación**. Decisión: bajo el ranking de margen, una **línea de aviso**
("N productos sin costo capturado — excluidos del margen") con la lista de esos productos accesible.
- Rationale: F7/criterios de aceptación lo exigen — Silvin debe entender _por qué_ su producto top de
  ventas no aparece en margen (para ir a capturar el costo), en vez de creer que "no vende". Se apoya
  en el patrón `Alert`/nota muted del DS, no inventa nada.

### D6 — Señal de muestra y umbral: el diseño **muestra** la señal, tech **fija** el número
Insight 4 (N de segundas visitas que respaldan cada producto) e Insight 5 (soporte mínimo, umbral)
requieren comunicar confianza. La UX **reserva el espacio** para esa señal en cada fila (conteo de
respaldo + fuerza) y define el **estado de "muestra insuficiente"**; el **valor del umbral y la
forma exacta de la fuerza** (co-ocurrencia simple vs. lift/confidence) los fija la tech-spec con
datos reales de Vanta (T3, P5 del PRD). El diseño acepta cualquiera de las dos: se presenta como
"fuerza de asociación" + "N ventas de respaldo", agnóstico al método.
</content>
</invoke>
