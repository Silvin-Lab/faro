# Handoff a ingeniería — Insights (insights)
_Autor: product-designer · Fecha: 2026-07-27 · Módulo: M9 · Gate: handoff completo_
_Fuentes: prd.md (aprobado, Silvin 2026-07-27) · wireframes.md · research.md · design-system Faro v0.3 (+M8)_

> Alcance de este handoff: **UI y comportamiento**. NO define modelo de datos, contratos de API ni el
> método estadístico (eso es tech-spec — ver T1–T5 del PRD). Las rutas son propuestas; tech puede
> ajustarlas. El módulo es **solo lectura** y **sin IA/LLM** (requisito no funcional del PRD).

## 0. Convenciones heredadas (no reinventar)
Toda la UI reusa lo ya construido en `faro-ui`:
- Componentes: `components/ui/{Card,Button,Input,Select,DateInput,StatusBadge,Alert}.tsx` y el
  lenguaje visual de `app/(app)/reports/page.tsx`.
- Tokens Tailwind ya mapeados: `text-ink`, `text-muted`, `text-danger`, `bg-accent`,
  `bg-accent-strong`, `bg-surface`, `bg-bg`, `border-line`. (= design-system v0.3 +M8.)
- Layout de página: header row (`h1 text-2xl font-semibold text-ink` + sub-línea
  `text-sm/xs text-muted` + acciones a la derecha), luego `Card`(s) en `space-y-4`.
- Dinero: centavos → pesos con `toPesos` (`@/lib/products`), siempre `tabular-nums`.
- Barras proporcionales, chips de %/conteo y tabla estilo Reportes (§ ese archivo). No inventar
  gráficas nuevas: Insights es "tarjetas legibles", no dashboard de charts (principio del PRD).

## 1. Navegación — sección "Insights" (separada de Reportes)

### 1.1 Sidebar (`components/Sidebar.tsx`)
Agregar una entrada **Insights** → **una sola ruta `/insights`** (D1). Debe quedar **separada de
Reportes** (el PRD lo pide: es comportamiento, no cifras operativas del día).

| Perfil | Ubicación propuesta | Nota |
|---|---|---|
| `super_admin` | **Sección nueva "Insights"** en el sidebar agrupado (un ítem), después de "Operación" o "Clientes" | Honra "sección nueva … separada de Reportes" del PRD |
| `branch_admin` | Ítem en el menú plano, junto a "Reportes" | Acotado a su sucursal por el servidor (ver §1.2) |
| `cashier` / `barista` | **No la ven** | Igual que Reportes hoy |

- Icono sugerido: `Lightbulb` (lucide). **Evitar `Sparkles`** — el PRD descarta toda connotación de IA.
- Estado activo del item = pastilla lime (`bg-accent text-ink`), igual que hoy.
- Ruta: `/insights` (página única). Rutas de API las define tech-spec.

### 1.2 Gating de acceso — sigue el patrón de Reportes (T1 del PRD)
Replicar exactamente el gating de `app/(app)/reports/page.tsx`:
- `super_admin`: ve todo, **filtro de sucursal libre** (select "Todas / <sucursal> / Sin sucursal").
- `branch_admin`: accede **acotado a su sucursal activa** por el servidor; **sin** select de sucursal
  (igual que en Reportes hoy). Encabezado puede mostrar `Insights · {nombre sucursal}`.
- `cashier` / `barista`: sin acceso (fallback `Card` "No tienes acceso a los insights.").
- **T1 queda para tech-spec confirmar**; el diseño asume el mismo patrón que Reportes.

## 2. Selector compartido de rango + sucursal (Decisión D2)

Un solo selector arriba de la página gobierna los 6 insights (`[from, to)` + `tz` + `branchId`,
semántica idéntica a `internal/reports`).

- **Reutilizar el patrón de Reportes** (pills de rango + `Input type=date` "Desde/Hasta" + botón
  "Aplicar" + `Select` de sucursal para super admin). **Recomiendo extraer** ese bloque de Reportes a
  un componente compartido `components/ReportFilters.tsx` (o similar) con **presets configurables**
  (ver §6, aporte al DS).
- **Presets de Insights** (distintos de Reportes por el dominio de comportamiento):
  `Últimos 30 días` (**default**) · `Últimos 90 días` · `Personalizado`.
  Rationale en research.md D2 — un rango de "Hoy" dejaría casi todas las tarjetas vacías.
- **Sin auto-refresh de 60 s ni `RefreshRing`** (Insights no es tablero en vivo). Recarga al cambiar
  preset / sucursal, o al pulsar "Aplicar" en Personalizado.
- Validación de Personalizado idéntica a Reportes: `Desde ≤ Hasta`, mensaje danger si no.
- Al cambiar cualquier filtro se recalculan **las 6** tarjetas (cada una con su estado propio, §4).

## 3. Especificación por insight

Orden en la página = orden del PRD (1→6), para trazabilidad. Cada tarjeta es un `Card`.

### 3.1 Insight 1 — Recurrencia de clientes (F1, F2, F3, F3b)
- **Titular:** frase llana "≈X de cada 10 clientes vuelven" derivada de la tasa, en `text-lg/xl
  font-semibold`. Debajo: `Z% · N de M clientes con ≥2 compras` (**siempre** numerador/denominador,
  no solo el %). Solo clientes identificados.
- **Cohorte de nuevos (F2/F3):** tres mini-stats `30 / 60 / 90 días`, cada uno con su `%` y su
  respaldo `k/N nuevos`. Nota muted: "La ventana puede mirar más allá del rango si el cliente ya
  volvió" (F3 — la retención no se trunca por `to`).
- **Bucket anónimas (F3b, D4):** línea de contexto muted con **conteo** y **% del total de ventas**;
  separada del cálculo de la tasa. Copy: "Ventas anónimas: {n} ventas · {p}% del total (no cuentan
  para recurrencia)."
- **Estados:** *loading* muted; *vacío* (sin clientes identificados) "Aún no hay clientes
  identificados en este rango." **conservando el bucket de anónimas si existe**; *error* danger.

### 3.2 Insight 2 — Producto estrella (F4–F8)
- **Tres rankings lado a lado** (`grid sm:grid-cols-3`, apilan en angosto): **Por ingresos** (`$`),
  **Por volumen** (`u`), **Por margen** (`$`). Cada uno etiquetado (F8). Top 5 por columna; fila =
  `rank · producto · valor` + **barra proporcional al máximo de su columna** (mismo estilo que el
  desglose por categoría de Reportes). `tabular-nums`.
- **Aviso "sin costo capturado" (F7, D5):** bajo el ranking de margen, un `Alert variant="warning"`
  (o línea muted con icono) con **conteo + lista** de productos excluidos del margen por
  `packageCostCents` nulo **o sin receta definida**. Nunca asumir costo 0 ni margen 100%. Copy:
  "{n} productos sin costo capturado — excluidos del margen: {lista}."
- **Estados por ranking:** *vacío* "Sin ventas en el rango."; el de **margen** puede estar vacío con
  ventas presentes → "Aún no hay productos con costo de receta completo." (+ el aviso lista cuáles).
  *loading* / *error* estándar.

### 3.3 Insight 3 — Ticket promedio por segmento (F9, F10)
- **Tres columnas de stat:** **Nuevo · Recurrente · Anónimas** (`grid sm:grid-cols-3`). Cada una:
  `$ promedio` grande + label + **"N ventas"** de respaldo (F10 — promedio sin volumen engaña).
- Nuevo/recurrente = **misma definición que Insight 1** (una nota muted lo hace explícito; sin dobles
  criterios). **Anónimas = tercer segmento explícito** (F9, D4), nunca sumado a los otros.
- Contraste opcional (lectura de negocio): "El recurrente gasta ~{p}% más que el nuevo por ticket."
- **Estados:** segmento sin ventas → "— · 0 ventas" (no rompe). *loading* / *error* estándar.

### 3.4 Insight 4 — Qué se pide en la 2ª visita (F11–F13b)
- **Lista** de productos sobre-representados en la visita #2 vs. promedio general, **ordenada por la
  desproporción**. Fila = `producto · fuerza de sobre-representación · respaldo`. **Sin selector de
  N** (F12: N=2 fijo).
  - *Fuerza:* p. ej. "1.9× vs. promedio" — **forma exacta la fija tech** (D6/T3), el diseño solo
    reserva el espacio para una medida de desproporción.
  - *Respaldo:* "en {k} de {N} segundas visitas" (señal de tamaño de muestra por producto — criterio
    de aceptación).
- **Nota al pie (F13b, D4):** "No incluye ventas anónimas (sin cliente no se puede secuenciar
  visitas)." — información muted, **no** un error.
- **Estados:** *muestra insuficiente* con copy propio (no conclusiones espurias): "Muestra
  insuficiente: solo {N} clientes tienen una 2ª visita en este rango. Amplía el rango." El umbral de
  "insuficiente" lo fija tech (D6). *loading* / *error* estándar.

### 3.5 Insight 5 — Afinidad de canasta (F14–F16)
- **Lista** del top de pares. Fila = `A + B · fuerza de asociación · respaldo (nº ventas juntas)`
  (F15). Ordenada por fuerza.
  - *Fuerza:* método (co-ocurrencia simple vs. lift/confidence) y forma exacta los fija tech (T3/P5,
    D6). El diseño la presenta como etiqueta + medida (p. ej. "Alta (2.4×)"), agnóstico al método.
  - *Umbral mínimo de soporte* (F16): aplicado en query; pares por debajo **no se muestran**. Valor
    concreto = tech con datos reales (P5). La UI solo enuncia "Solo se muestran pares con respaldo
    suficiente."
- Anónimas cuentan con normalidad (nivel venta/línea) — **sin** nota de exclusión.
- **Estados:** *vacío / insuficiente* "Aún no hay pares de productos con suficiente respaldo en este
  rango." *loading* / *error* estándar.

### 3.6 Insight 6 — Efectividad de lealtad (F17–F19)
- **Comparación dos columnas:** "Canjeó lealtad" vs. "No canjeó". Filas: **Ticket promedio · Ventas
  por cliente** (proxy de recurrencia) **· Gasto por cliente** (F18). Columna de **contraste**
  (▲/▼ % o `×`) para leer el efecto de un golpe.
- Segmento "canjeó" = `sales.loyalty_reward IS NOT NULL` (T5 — hecho del esquema; tech confirma
  nombre/tipo real de la columna). Solo clientes identificados (F19).
- **Respaldo:** "Basado en {a} clientes que canjearon · {b} que no."
- **Nota al pie (F19, D4):** "No incluye ventas anónimas (sin cliente no hay canje que atribuir)." —
  información muted, no error.
- **Estados:** *segmento vacío* (sin canjes) → "Sin canjes de lealtad en este rango." en la columna
  "Canjeó", **conservando** la columna "No canjeó" (criterio de aceptación). *loading* / *error*
  estándar.

## 4. Comportamiento transversal
- **Carga aislada por tarjeta (D3):** cada insight resuelve **carga / vacío / muestra insuficiente /
  error** por su cuenta; un insight que falla no tumba la página (mismo patrón que el error aislado
  de "Gastos" en Reportes). Recomendado: cada tarjeta hace su propia request o el endpoint devuelve
  un objeto por insight que puede venir con `error`/`insufficient` sin romper el resto.
- **Estados vacío vs. muestra insuficiente — distinción intencional:** "vacío" = no hubo datos en el
  rango; "muestra insuficiente" = hubo datos pero muy pocos para concluir (Insight 4/5). Copy
  distinto (§3), nunca un número espurio.
- **Anónimas (D4):** en 1 y 3 = **segmento/contexto visible**; en 4 y 6 = **nota al pie muted** de
  exclusión estructural; en 2 y 5 = sin tratamiento (cuentan normal). Nunca exclusión en silencio en
  1/3.
- **Números:** `tabular-nums` en todo dato; dinero via `toPesos`; porcentajes redondeados a entero;
  `×`/`▲▼` para contraste. Números negativos (si aplican en contraste) en `text-danger`.
- **Accesibilidad:** cada control del filtro con `<label>`; foco visible; contraste AA (texto sobre
  lime siempre oscuro); las barras proporcionales con `aria-label` textual como en Reportes; áreas
  táctiles ≥44px (tablet).
- **Responsive:** rankings de 3 columnas (Insight 2) y stats de 3 columnas (Insight 3) apilan en
  angosto (`grid` → 1 col); comparación de Insight 6 se vuelve bloques apilados. Listas largas con
  `overflow-x-auto` + `min-w-[...]` como los historiales de Reportes.
- **Sin IA:** ninguna ruta llama a un LLM (requisito no funcional). Todo es render de agregados.

## 5. Trazabilidad requisitos → UI
| Req | Dónde |
|---|---|
| F1 tasa de recurrencia (num/den/%) | Insight 1, titular + línea de tasa |
| F2/F3 cohorte 30/60/90 (mira más allá de `to`) | Insight 1, mini-stats + nota |
| F3b bucket anónimas (contexto) | Insight 1, línea de contexto muted |
| F4/F5/F6/F8 tres rankings etiquetados | Insight 2, 3 columnas |
| F7 "sin costo capturado" visible | Insight 2, `Alert` bajo ranking de margen |
| F9/F10 ticket 3 segmentos + conteo | Insight 3, 3 columnas de stat |
| F11–F13 sobre-representación visita #2 (N=2 fijo) | Insight 4, lista ordenada por desproporción |
| F13b anónimas excluidas (estructural) | Insight 4, nota al pie muted |
| F14/F15 top de pares + fuerza + respaldo | Insight 5, lista |
| F16 umbral mínimo de soporte | Insight 5, filtrado en query + enunciado |
| F17/F18 canjeó vs. no (ticket/recurrencia/gasto) | Insight 6, comparación 2 columnas |
| F19 anónimas excluidas (estructural) | Insight 6, nota al pie muted |
| Selector rango+sucursal compartido | §2, filtro extraído del patrón de Reportes |
| Sección "Insights" separada de Reportes | §1, sidebar |
| Estados vacío / muestra insuficiente | §3/§4, por tarjeta |

## 6. Aportes al design system (foundations)
Documentados en `foundations/design-system.md` (§ M9 Insights). Nuevos patrones a formalizar:
1. **Filtro rango + sucursal reutilizable** — extraer el bloque de Reportes a un componente
   compartido con **presets configurables** (Insights: 30/90/Personalizado; Reportes: Hoy/Ayer/
   Personalizado). Misma semántica `[from,to)`+`tz`+`branchId`.
2. **Tarjeta de insight** — `Card` con estructura titular llano → detalle → estados por-tarjeta.
3. **Stat / KPI block** — label muted + número grande `tabular-nums` + sub-línea de respaldo.
4. **Ranking list con barra** — fila `rank · label · valor` + barra proporcional (generaliza el
   desglose por categoría de Reportes).
5. **Comparación por segmentos** — 2–3 columnas de stat con fila de contraste (▲/▼/×).
6. **Estado "muestra insuficiente"** — variante de estado vacío con copy específico (distinta de
   "vacío").

## 7. Notas abiertas para tech-spec (no son diseño)
- **T1 — Gating:** confirmar super_admin + branch_admin acotado; cashier/barista sin acceso (§1.2).
- **T2 — En vivo vs. pre-cómputo por insight:** afinidad (5) y cohortes (1) son las candidatas a
  pesar. La UI no cambia según la decisión (la carga aislada por tarjeta, §4, soporta ambos).
- **T3 — Método de afinidad (5) y forma de la fuerza (4/5):** co-ocurrencia vs. lift/confidence. El
  diseño es agnóstico: presenta "fuerza + respaldo" (§3.4/§3.5).
- **T4 — Costo de receta (2):** cómo se resuelve el costo desde `supplies` y el manejo de
  `packageCostCents` nulo a nivel de query; el diseño solo exige que el excluido sea **visible**
  como "sin costo capturado" (F7).
- **T5 — Canje de lealtad (6):** implementar "canjeó" como `sales.loyalty_reward IS NOT NULL`;
  confirmar nombre/tipo real de la columna.
- **Umbrales concretos** (soporte mínimo de afinidad, corte de "muestra insuficiente" en visita #2):
  se calibran con datos reales de Vanta en tech-spec/QA (P5). El diseño reserva el espacio; no impone
  el número.
</content>
