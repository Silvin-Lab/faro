# PRD — Insights (insights)
_Autor: project-manager · Fecha: 2026-07-27 · Estado: **aprobado** (Silvin, 2026-07-27) · Módulo: M9 · Depende de: sales-reports (existente), loyalty, supplies_

## Problema
Faro tiene reportes de **cifras operativas** (ventas y gastos, `internal/reports`): cuánto se
vendió, por categoría, por horario, por sucursal. Pero no responde preguntas de **comportamiento**
que Silvin necesita para tomar decisiones de negocio: ¿qué porcentaje de clientes vuelve?, ¿cuál es
el producto estrella y bajo qué definición (ingresos, volumen o margen real)?, ¿el cliente
recurrente gasta más que el nuevo?, ¿hay patrones de compra ligados al ciclo de visitas (qué se
pide en la 2ª visita)?, ¿qué productos se compran juntos?, ¿la lealtad realmente mueve la aguja?

Hoy esas respuestas no existen en la plataforma; para obtenerlas habría que consultar la base de
datos a mano. Este módulo las sistematiza como una sección de **inteligencia de negocio
descriptiva**, calculada de forma **determinística** (agregación SQL/Go), sin depender de ningún
modelo de lenguaje.

## Decisión de negocio previa (Silvin, 2026-07-24/27)
- **Cero LLM en este módulo.** Todo el cálculo es agregación determinística (SQL/Go). No hay
  llamadas a la API de Anthropic ni a ningún modelo. Es un gasto que Silvin decidió no asumir en
  esta fase. Cualquier propuesta de "narración por IA", chat sobre los datos o texto generado está
  **explícitamente descartada** de este PRD.
- La capa de narración en lenguaje natural sobre las métricas ya calculadas queda **pospuesta**
  (no descartada) como fase futura, fuera de este módulo.

## Usuarios y casos de uso
- **Dueño del negocio (Silvin) / admin:** usa Insights para decidir qué productos empujar o retirar,
  cómo diseñar promociones (qué se compra junto, qué se pide en la 2ª visita), y si el programa de
  lealtad justifica su costo. Es el consumidor principal.
- **Encargado / gerente de sucursal:** consulta el mismo tablero acotado a su sucursal (según el
  gating de acceso que se confirme, ver Pendiente para tech-spec).

> **Gating de acceso:** consistente con `internal/reports`, se asume acceso para **super admin**
> (filtro de sucursal libre) y, si aplica, **branch_admin** forzado a su sucursal activa. Roles
> `cashier`/`barista` no acceden. Confirmar al momento de implementar (ver Pendiente para tech-spec).

Casos de uso principales:
1. Medir **retención**: qué porción de clientes vuelve, y qué tan rápido vuelven los nuevos.
2. Identificar el **producto estrella** según el criterio relevante (ingresos, volumen o margen).
3. Comparar el **valor** del cliente recurrente vs. el nuevo (ticket promedio).
4. Detectar **qué se pide en la visita N** para diseñar incentivos de retorno.
5. Detectar **qué productos se compran juntos** para combos/ubicación en menú.
6. Evaluar si la **lealtad** cambia el comportamiento de gasto/recurrencia.

## Objetivos y métricas de éxito
- Silvin puede responder las 6 preguntas anteriores desde la plataforma, sin tocar la base de datos.
- Cada insight es **accionable**: deja claro qué producto/segmento/par mirar, no solo un número.
- Todos los insights respetan el **mismo rango de fechas y filtro de sucursal** que sales-reports,
  para lectura consistente con el resto de la plataforma.
- Cálculo determinístico y reproducible: mismos filtros ⇒ mismo resultado, sin dependencia de IA.

## Alcance y navegación (cerrado en el brief — Silvin, 2026-07-27)
- **Los 6 insights entran completos en v1.** No se parte en fases (misma familia de queries sobre
  las mismas tablas).
- **Sección nueva "Insights" en el sidebar**, separada de "Reportes" (es comportamiento, no cifras
  operativas del día a día).
- **Mismo selector de rango de fechas y sucursal** que ya usa sales-reports (`from`/`to` +
  `branchId`; super admin filtra libre, branch_admin acotado a su sucursal activa). Consistencia con
  el resto de la plataforma.

## Definiciones canónicas (aplican a todos los insights)
Para evitar interpretaciones divergentes entre insights, el PRD fija estas definiciones. Todas
fueron confirmadas por Silvin (2026-07-27).

- **Cliente identificado:** venta con `sales.customer_id` no nulo. **Venta anónima:** venta de
  mostrador sin cliente (`customer_id` nulo).
- **Tratamiento de ventas anónimas — diferenciado por insight** (no hay una regla única de
  exclusión; cerrado por Silvin 2026-07-27):
  - **Insights 4 (visita-N) y 6 (lealtad):** las ventas anónimas quedan **excluidas por restricción
    estructural de los datos**, no por decisión de negocio: sin `customer_id` no existe un cliente al
    que atribuir la secuencia de visitas ni el canje de lealtad, así que es **imposible** incluirlas.
  - **Insights 1 (recurrencia) y 3 (ticket promedio):** las ventas anónimas se muestran como un
    **tercer bucket separado y explícito, "ventas anónimas"**, con su propio conteo y ticket
    promedio. **No** se excluyen en silencio ni se mezclan con nuevo/recurrente; son contexto visible
    (p. ej. "X% de tus ventas son anónimas, con este ticket promedio").
  - **Insights 2 (producto estrella) y 5 (afinidad de canasta):** operan a nivel de venta/línea, no de
    cliente, así que las ventas anónimas **sí cuentan** con normalidad.
- **Recurrente (dentro del período):** cliente con **≥2 ventas** en el rango seleccionado.
- **Nuevo (dentro del período):** cliente cuya **primera venta histórica** (sobre todo su historial,
  no solo la ventana) cae **dentro** del rango seleccionado (confirmado, ver Insight 1).
- **Período / filtro:** todo insight se calcula sobre `[from, to)` y el `branchId` activos, con la
  misma semántica de `internal/reports` (incluida la zona horaria `tz` que ya maneja el reporte de
  ventas).
- **Dinero:** montos en centavos (`*_cents`), consistente con el resto del sistema.

## Requisitos funcionales

### Insight 1 — Recurrencia de clientes
- **F1** Mostrar la **tasa de recurrencia** del período: `clientes con ≥2 ventas / clientes con ≥1
  venta`, ambos sobre **clientes identificados** en el rango. Mostrar numerador, denominador y
  porcentaje (no solo el %). La tasa **no** incluye ventas anónimas (no tiene sentido "recurrencia"
  sin cliente).
- **F2** Mostrar el análisis de **cohorte de nuevos**: de los clientes cuya primera venta cae en el
  período, qué porcentaje **vuelve** dentro de **30 / 60 / 90 días** desde su primera compra.
- **F3** La ventana de retorno (30/60/90) **puede extenderse más allá de `to`** si el dato existe:
  la cohorte se define por fecha de alta dentro del período, pero el "¿volvió?" mira el historial
  real del cliente (la métrica no queda truncada por `to` cuando ya hay datos posteriores). Confirmado.
- **F3b** Mostrar, junto a la recurrencia, un **tercer bucket "ventas anónimas"** como contexto
  explícito: su **conteo** y su participación sobre el total de ventas del período (p. ej. "X% de tus
  ventas son anónimas"). No se mezcla con el cálculo de la tasa de recurrencia, pero es visible en el
  mismo insight para dar contexto de cuánta actividad no es atribuible a un cliente.

**Criterios de aceptación (Insight 1)**
- [ ] Con un rango dado veo `X de Y clientes volvieron (Z%)` para la tasa de recurrencia, calculada
      solo sobre clientes identificados.
- [ ] Veo el % de la cohorte de nuevos que regresó a 30, 60 y 90 días.
- [ ] Veo un bucket explícito de **ventas anónimas** (conteo y % del total) como contexto; **no** se
      excluye en silencio ni se mezcla con el cálculo de recurrencia.
- [ ] Con un rango sin clientes identificados, el insight muestra un estado vacío claro (no un error
      ni una división por cero) y, si hay ventas anónimas, las sigue mostrando en su bucket.

### Insight 2 — Producto estrella
- **F4** Ranking de productos por **ingresos** (suma de `total_cents` de sus líneas en el período).
- **F5** Ranking de productos por **volumen** (unidades vendidas en el período).
- **F6** Ranking de productos por **margen real**, usando el **costo de receta** derivado de
  `internal/supplies` (costo de insumos por presentación, `packageCostCents`). Margen = ingreso −
  costo de insumos de la receta.
- **F7** **Regla de exclusión de margen:** si algún insumo de la receta tiene `packageCostCents`
  nulo (costo no capturado), el producto **queda fuera** del ranking de margen — **no** se asume
  costo 0. El producto debe indicarse como "sin costo capturado" en algún lugar visible para que
  Silvin sepa por qué no aparece, en vez de desaparecer silenciosamente.
- **F8** Los tres rankings son visibles y claramente etiquetados (un producto puede liderar
  ingresos pero no margen; ese contraste es la parte accionable).

**Criterios de aceptación (Insight 2)**
- [ ] Veo el top de productos por ingresos, por volumen y por margen, cada uno etiquetado.
- [ ] Un producto con un insumo de `packageCostCents` nulo **no** aparece en el ranking de margen y
      figura como "sin costo capturado" (no asume costo 0 ni margen 100%).
- [ ] Un producto **sin receta definida** queda excluido del ranking de margen (presente en
      ingresos/volumen), igual que el caso de costo nulo (P3, aprobado).
- [ ] Los rankings respetan el rango de fechas y el filtro de sucursal.

### Insight 3 — Ticket promedio: nuevo vs. recurrente
- **F9** Mostrar el **ticket promedio** (ingreso por venta) en **tres segmentos**: **cliente nuevo**,
  **cliente recurrente** y **ventas anónimas**, usando las definiciones canónicas, dentro del período.
  El bucket de anónimas es un segmento propio y visible, no un descarte silencioso.
- **F10** Mostrar el **conteo de ventas** de cada uno de los tres segmentos junto a su promedio (un
  promedio sin volumen de respaldo es engañoso).

**Criterios de aceptación (Insight 3)**
- [ ] Veo ticket promedio de **nuevos**, **recurrentes** y **ventas anónimas**, cada uno con su
      número de ventas de respaldo.
- [ ] La segmentación nuevo/recurrente usa exactamente la misma definición que el Insight 1 (sin
      dobles criterios).
- [ ] Las ventas anónimas aparecen como **tercer bucket explícito** con su propio ticket promedio y
      conteo; **no** se excluyen en silencio ni se suman a nuevo/recurrente.

### Insight 4 — Correlación visita-N ↔ producto
- **F11** Numerar las visitas de cada cliente con `ROW_NUMBER() OVER (PARTITION BY customer_id ORDER
  BY created_at)` y detectar qué **productos aparecen desproporcionadamente en la visita #2**
  respecto al promedio general de todas las visitas.
- **F12** **La visita analizada es N = 2, fija en v1** (qué se pide en la segunda visita). **Sin
  configurabilidad de N en la UI** (confirmado por Silvin, 2026-07-27). Otros N quedan como
  posibilidad futura, fuera de v1.
- **F13** La numeración de visitas se calcula sobre el **historial completo** del cliente para que
  "visita #2" signifique realmente su segunda visita, y el resultado se **presenta** para el período
  seleccionado. La interacción exacta entre numeración global y ventana del período la fija la
  tech-spec, respetando esta intención de negocio.
- **F13b** Solo aplica a **clientes identificados**: las ventas anónimas quedan **excluidas por
  restricción estructural** (sin `customer_id` no se puede secuenciar visitas), no por elección de
  negocio.

**Criterios de aceptación (Insight 4)**
- [ ] Veo, para la **visita #2**, los productos con sobre-representación respecto al promedio general,
      ordenados por esa desproporción. (No hay selector de N en la UI.)
- [ ] Cada producto listado muestra una señal de tamaño de muestra (p. ej. cuántas visitas #2 lo
      incluyen), para no accionar sobre ruido.
- [ ] Las ventas anónimas no aparecen en este insight por imposibilidad de atribución (no es una
      exclusión opcional).
- [ ] Con pocos datos, el insight muestra un estado de "muestra insuficiente" en vez de conclusiones
      espurias.

### Insight 5 — Afinidad de canasta
- **F14** Detectar **pares de productos** que aparecen juntos en el mismo `sale_id` con más
  frecuencia de la que el azar explicaría (co-ocurrencia). El **método estadístico** (co-ocurrencia
  simple vs. lift/confidence) lo decide la tech-spec según el volumen real de datos; el PRD exige el
  **resultado de negocio**: una lista de pares accionables.
- **F15** Mostrar el **top de pares** con una medida de fuerza de la asociación y el conteo de
  ventas que respalda cada par.
- **F16** Aplicar un **umbral mínimo de soporte** (nº mínimo de ventas conjuntas) para no mostrar
  pares anecdóticos. El valor concreto lo fija la tech-spec/QA sobre datos reales. → parámetro a
  confirmar (P5).

**Criterios de aceptación (Insight 5)**
- [ ] Veo un top de pares de productos comprados juntos, con su fuerza de asociación y el nº de
      ventas que lo respalda.
- [ ] Pares por debajo del umbral mínimo de soporte no se muestran.
- [ ] Respeta rango de fechas y filtro de sucursal.

### Insight 6 — Efectividad de lealtad
- **F17** Comparar **gasto** y **recurrencia** entre dos segmentos: clientes que **canjearon** al
  menos una recompensa de lealtad en el período vs. clientes que **no**. El segmento "canjeó" se
  define sobre el propio esquema: **`sales.loyalty_reward IS NOT NULL`** marca una venta con canje
  (hecho del esquema, no una decisión de negocio; ver T5).
- **F18** Mostrar, por segmento: ticket promedio, nº de ventas por cliente (proxy de recurrencia) y
  gasto total/promedio por cliente, para que el contraste sea legible.
- **F19** Solo aplica a **clientes identificados**: las ventas anónimas quedan **excluidas por
  restricción estructural** (sin `customer_id` no hay cliente al que atribuir el canje ni comparar
  su comportamiento), no por elección de negocio.

**Criterios de aceptación (Insight 6)**
- [ ] Veo ticket promedio, recurrencia y gasto por cliente para el segmento "canjeó lealtad" vs.
      "no canjeó".
- [ ] El segmento "canjeó" se define sobre canjes reales (`sales.loyalty_reward IS NOT NULL`), no
      sobre mera inscripción/acumulación de puntos.
- [ ] Las ventas anónimas no participan del insight por imposibilidad de atribución.
- [ ] Con un segmento vacío (p. ej. sin canjes en el período), el insight lo indica sin romperse.

## Requisitos no funcionales
- **Determinismo / sin IA:** ninguna ruta del módulo llama a un LLM. Mismos filtros ⇒ mismo
  resultado.
- **Solo lectura:** el módulo no muta datos de ventas, clientes, insumos ni lealtad; solo agrega y
  presenta. Sigue el patrón de `internal/reports` (store + service de solo lectura).
- **Rendimiento:** el cálculo en vivo es aceptable mientras el volumen de Vanta lo permita (como hace
  `reports` hoy). Si algún insight pesado (afinidad de canasta, cohortes) degrada la respuesta sobre
  datos reales, la tech-spec decide pre-cómputo/cache (ver Pendiente para tech-spec). El PRD no fija
  una arquitectura de cache a priori.
- **Consistencia de filtros:** reutiliza la semántica de scope de `internal/reports`
  (tenant + `[from,to)` + `BranchFilter` + `tz`).
- **Coherencia visual:** respeta el design system existente de Faro (la definición de UI la produce
  product-designer a partir de este PRD).

## Alcance

### En alcance (v1)
- Los **6 insights** descritos (Recurrencia, Producto estrella, Ticket nuevo vs. recurrente,
  Correlación visita-N, Afinidad de canasta, Efectividad de lealtad), todos calculados de forma
  determinística.
- Sección **"Insights"** nueva en el sidebar, separada de "Reportes".
- Selector de **rango de fechas + sucursal** reutilizando el patrón de sales-reports.
- Estados vacíos / de muestra insuficiente claros en cada insight.

### Fuera de alcance / futuro
- **Cualquier narración, chat o texto generado por LLM** sobre los datos (decisión de negocio
  explícita; pospuesto, no descartado).
- **Insights predictivos** (forecasting de ventas, predicción de churn con ML). Este módulo es
  **descriptivo**, no predictivo.
- **Comparación entre negocios/tenants** (Faro es single-tenant hoy).
- **Exportación** (CSV/PDF) de los insights — candidato futuro, no requerido en v1.
- **Notificaciones/alertas** proactivas basadas en insights — futuro.
- **Reglas de asociación completas** (algoritmos tipo Apriori/FP-growth con confidence/lift
  multi-ítem): v1 se queda en pares y co-ocurrencia; la sofisticación es futura si el volumen lo
  amerita.

## Dependencias
- **sales-reports (`internal/reports`, existente en prod):** patrón de scope
  (tenant + rango + sucursal + tz), estilo store/service de solo lectura y el selector de filtros de
  la UI. Referencia a seguir, **no** a modificar.
- **ventas (`sales`):** `sales.customer_id`, `sales.created_at`, `sale_id`, líneas de venta y
  `total_cents` — base de recurrencia, ticket, visita-N y afinidad.
- **supplies:** costo de receta por presentación (`packageCostCents`) para el ranking de margen del
  Insight 2.
- **loyalty:** redenciones de recompensa (`loyalty_reward`) para el Insight 6.
- **business-settings / sucursales:** catálogo de sucursales para el filtro `branchId`.

## Preguntas de producto — RESUELTAS (Silvin, 2026-07-27)
No queda ninguna pregunta de producto abierta. Registro de cómo se cerró cada una:

- **P1 — Ventas anónimas → tratamiento diferenciado por insight (no exclusión única).**
  - Insights **4 y 6**: excluidas por **restricción estructural** de los datos (sin `customer_id` es
    imposible atribuir visita o canje), no por preferencia.
  - Insights **1 y 3**: se muestran como **tercer bucket explícito "ventas anónimas"** con su conteo y
    ticket promedio; no se excluyen en silencio ni se mezclan con nuevo/recurrente.
  - Insights **2 y 5**: cuentan con normalidad (operan a nivel de venta/línea).
  - Reflejado en Definiciones canónicas, F1–F3b, F9–F10, F13b y F19.
- **P2 — Definición de "nuevo" y ventana de cohorte → aprobado el default.** "Nuevo" = primera venta
  histórica dentro del período; la retención 30/60/90 puede mirar datos posteriores a `to`. Ver F2–F3.
- **P3 — Producto sin receta / costo nulo en ranking de margen → aprobado el default.** Queda **fuera**
  del ranking de margen y se marca "sin costo capturado"; nunca se asume costo 0. Ver F6–F8.
- **P4 — Insight visita-N → N=2 fijo, sin configurabilidad en la UI en v1.** Ver F11–F12.
- **P5 — Afinidad de canasta: umbral y tamaño de lista → aprobado el default.** Se fija con **datos
  reales de Vanta** en tech-spec/QA (sin número de negocio impuesto). Ver F16 y T3.
- **P6 — Mapeo de "canjeó lealtad" → resuelto por el esquema, movido a tech-spec (T5).** El propio
  esquema lo responde: **`sales.loyalty_reward IS NOT NULL` = canjeó**; es un hecho del modelo, no una
  decisión de negocio. Ver F17.

## Pendiente para tech-spec (no bloquea el gate del PRD — lo resuelve tech-lead/backend)
- **T1 — Gating de acceso:** confirmar que el acceso replica `internal/reports` (super admin +
  branch_admin acotado; cashier/barista 403). El brief asumía "solo super admin"; el código de
  reports ya admite branch_admin. Alinear al implementar.
- **T2 — Volumen de datos real de Vanta:** verificar por query directa cuántas ventas/clientes hay en
  prod para decidir **cálculo en vivo vs. pre-cómputo/cache** por insight (afinidad de canasta y
  cohortes son los candidatos a pesar). El PRD no impone arquitectura de cache.
- **T3 — Método estadístico de afinidad:** co-ocurrencia simple vs. lift/confidence, según volumen
  (F14).
- **T4 — Costo de receta:** cómo se resuelve el costo de insumos por producto desde `supplies`
  (join de receta) y el manejo de insumos con `packageCostCents` nulo a nivel de query (F6/F7).
- **T5 — Definición de canje de lealtad (Insight 6):** implementar el segmento "canjeó" como
  **`sales.loyalty_reward IS NOT NULL`** (hecho del esquema, ya cerrado — no requiere decisión de
  negocio). Verificar el nombre/tipo real de la columna en el esquema de `sales` al implementar.

## Gate
**PRD aprobado (Silvin, 2026-07-27) — listo para diseño y tech-spec.** Alcance de v1 (6 insights,
sección propia, filtros de sales-reports) cerrado por el brief, y las **6 preguntas de producto
(P1–P6) quedaron resueltas**: no hay ninguna decisión de negocio pendiente. Lo que resta (T1–T5) es
técnico y se resuelve en la tech-spec. Siguiente artefacto: diseño (product-designer) y tech-spec
(tech-lead), en paralelo según priorice Silvin.
