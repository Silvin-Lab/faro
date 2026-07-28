# Wireframes — Insights (insights)
_Autor: product-designer · Fecha: 2026-07-27 · Módulo: M9 · Fuente: prd.md (aprobado) · research.md_

Wireframes de baja fidelidad (estructura, jerarquía y estados). El look final se hereda del design
system de Faro (BrightPOS lime) y de los componentes existentes (`Card`, `Button`, `Input`,
`Select`, `DateInput`, `StatusBadge`, `Alert`, `DistributionChart`, tablas y listas de Reportes).
Las specs de estados/comportamiento y tokens van en `handoff.md`.

Convención de la maqueta: una **tarjeta de insight** = un `Card` que abre con un **titular en
lenguaje llano** (la lectura de negocio), seguido del detalle de respaldo (conteos, barras, señal de
muestra) y sus estados. No es un dashboard de gráficas abstractas (principio del PRD).

## Mapa de navegación

Nueva entrada **Insights** en el sidebar, **separada de Reportes** (comportamiento, no cifras del
día). Una sola ruta con las 6 tarjetas apiladas bajo un selector compartido (Decisión D1).

```
INSIGHTS                        (sección nueva; super admin la ve como grupo, branch admin como ítem)
└─ Insights          /insights  → 6 tarjetas de insight + selector rango/sucursal compartido
```

Relación con Reportes:
```
Reportes  = cifras operativas del día (cuánto se vendió, por categoría/horario/sucursal)
Insights  = comportamiento (quién vuelve, qué deja margen, qué se compra junto, si la lealtad sirve)
             ↑ mismo selector de rango + sucursal, misma semántica [from,to)+tz+branchId
```

---

## Estructura de página (`/insights`)

```
┌─────────────────────────────────────────────────────────────────────────────┐
│  Insights                                    [ Todas las sucursales ▾ ]       │  ← header + filtro sucursal (super admin)
│  Comportamiento de tus clientes y productos. │ [30 días][90 días][Personaliz.]│  ← pills de rango (default 30 días)
│                                                                               │
│  ┌── (solo si "Personalizado") ────────────────────────────────────────┐     │
│  │  Desde [ 2026-06-27 ]   Hasta [ 2026-07-27 ]   [ Aplicar ]           │     │  ← idéntico a Reportes
│  └─────────────────────────────────────────────────────────────────────┘     │
│                                                                               │
│  ▸ 1. Recurrencia de clientes            (Card)                               │
│  ▸ 2. Producto estrella                   (Card)                              │
│  ▸ 3. Ticket promedio por segmento        (Card)                             │
│  ▸ 4. Qué se pide en la 2ª visita         (Card)                             │
│  ▸ 5. Afinidad de canasta                 (Card)                             │
│  ▸ 6. Efectividad de lealtad              (Card)                             │
└─────────────────────────────────────────────────────────────────────────────┘
```
- Header row = patrón de Reportes: `h1 text-2xl font-semibold text-ink` + sub-línea muted; a la
  derecha, select de sucursal (solo super admin) + pills de rango.
- **Presets de rango**: `Últimos 30 días` (default) · `Últimos 90 días` · `Personalizado`
  (D2). Semántica `[from,to)` + `tz` + `branchId` = `internal/reports`. **Sin auto-refresh.**
- Orden de tarjetas = orden del PRD (1→6), para trazabilidad directa a F1–F19.
- Cada tarjeta carga y falla de forma **aislada** (D3): loading / vacío / muestra insuficiente /
  error por-tarjeta, sin tumbar el resto.

---

## Insight 1 — Recurrencia de clientes  (F1, F2, F3, F3b)

```
┌── Card ─────────────────────────────────────────────────────────────────────┐
│  Recurrencia de clientes                                                      │
│                                                                               │
│   3 de cada 10 clientes vuelven                                               │  ← titular llano
│   31%   ·   62 de 200 clientes con ≥2 compras                                 │  ← % + num/den (F1)
│                                                                               │
│  ─────────────────────────────────────────────────────────────────────────  │
│  Clientes nuevos que regresaron (cohorte del período)                         │  ← F2/F3
│   ┌───────────┐   ┌───────────┐   ┌───────────┐                               │
│   │  30 días  │   │  60 días  │   │  90 días  │                               │
│   │    42%    │   │    55%    │   │    61%    │                               │
│   │ 38/90 nuev│   │ 50/90     │   │ 55/90     │                               │
│   └───────────┘   └───────────┘   └───────────┘                               │
│   La ventana puede mirar más allá del rango si el cliente ya volvió.          │  ← nota muted (F3)
│                                                                               │
│  · Ventas anónimas: 320 ventas · 28% del total (no cuentan para recurrencia)  │  ← bucket contexto (F3b)
└───────────────────────────────────────────────────────────────────────────────┘
```
- **Titular** = frase "X de cada 10…" derivada de la tasa; debajo, el % exacto y numerador/
  denominador (nunca solo el %). Solo clientes identificados (F1).
- **Cohorte 30/60/90** = tres mini-stats con % y su `k/N nuevos` de respaldo (F2). Nota muted aclara
  que la ventana puede extenderse más allá de `to` (F3).
- **Bucket anónimas** = línea de contexto muted con conteo y % del total; **separada** del cálculo
  de recurrencia (F3b, D4). No se mezcla ni se excluye en silencio.
- **Estados:**
  - *loading*: "Cargando…" (muted).
  - *vacío* (sin clientes identificados): "Aún no hay clientes identificados en este rango." — y si
    hay ventas anónimas, **igual se muestra su bucket** (criterio de aceptación Insight 1).
  - *error*: texto danger.

---

## Insight 2 — Producto estrella  (F4, F5, F6, F7, F8)

Tres rankings **lado a lado** (el contraste es lo accionable, D-principio). En móvil/tablet angosto
se apilan. Cada columna: top 5, `rank · producto · valor` + barra proporcional (lenguaje visual de
Reportes).

```
┌── Card ─────────────────────────────────────────────────────────────────────┐
│  Producto estrella                                                            │
│  Un producto puede liderar ingresos y no margen — ese contraste es el punto.  │
│                                                                               │
│  ┌ Por ingresos ────┐  ┌ Por volumen ─────┐  ┌ Por margen ──────┐            │
│  │1 Latte    $12,400│  │1 Espresso  820 u │  │1 Filtrado  $6,100│            │
│  │  ████████████    │  │  ████████████    │  │  ████████████    │            │
│  │2 Capp.    $9,800 │  │2 Latte     640 u │  │2 Latte     $5,200│            │
│  │  █████████       │  │  █████████        │  │  ██████████       │           │
│  │3 Filtrado $7,300 │  │3 Capp.     510 u │  │3 Capp.     $3,900│            │
│  │  ██████          │  │  ███████          │  │  ███████          │           │
│  │4 …               │  │4 …                │  │4 …                │           │
│  └──────────────────┘  └──────────────────┘  └──────────────────┘            │
│                                                                               │
│  ⚠ 3 productos sin costo capturado — excluidos del margen: Frappé, Té, Cookie │  ← F7 (D5)
└───────────────────────────────────────────────────────────────────────────────┘
```
- **Tres rankings etiquetados** (F4/F5/F6/F8): ingresos (`$`), volumen (`u`), margen (`$`). Barra
  proporcional al máximo de **su** columna. `tabular-nums`.
- **Aviso "sin costo capturado"** bajo el ranking de margen (F7, D5): banda `Alert` (o línea muted)
  con conteo + lista de los productos excluidos por insumo con `packageCostCents` nulo **o sin
  receta**. Nunca se asume costo 0. Es visible para que Silvin sepa por qué falta ese producto.
- **Estados:** por ranking, *vacío* "Sin ventas en el rango."; el de **margen** puede estar vacío
  aunque haya ventas: "Aún no hay productos con costo de receta completo." (+ el aviso lista cuáles).
  *loading* / *error* estándar.

---

## Insight 3 — Ticket promedio por segmento  (F9, F10)

Tres columnas de stat: **Nuevo · Recurrente · Anónimas**, cada una con su promedio y su conteo de
respaldo (un promedio sin volumen engaña, F10). El bucket anónimo es un **segmento propio** (D4).

```
┌── Card ─────────────────────────────────────────────────────────────────────┐
│  Ticket promedio por segmento                                                 │
│                                                                               │
│   ┌── Nuevo ──────┐   ┌── Recurrente ─┐   ┌── Anónimas ───┐                   │
│   │   $128        │   │   $164        │   │   $95         │                   │
│   │  ticket prom. │   │  ticket prom. │   │  ticket prom. │                   │
│   │  90 ventas    │   │  310 ventas   │   │  320 ventas   │                   │
│   └───────────────┘   └───────────────┘   └───────────────┘                   │
│                                                                               │
│  El recurrente gasta ~28% más que el nuevo por ticket.                        │  ← lectura de contraste (opcional)
│  Nuevo/recurrente usan la misma definición que Recurrencia.                   │  ← nota muted (coherencia con Insight 1)
└───────────────────────────────────────────────────────────────────────────────┘
```
- **Tres segmentos** con `$ promedio` (grande, tabular) + "N ventas" de respaldo (F9/F10). Misma
  definición de nuevo/recurrente que Insight 1 (nota lo hace explícito).
- **Anónimas = tercer segmento explícito** con su propio ticket y conteo (F9, D4); no se suma a los
  otros dos ni se descarta.
- **Estados:** un segmento sin ventas se muestra "— · 0 ventas" (no rompe). *loading* / *error*
  estándar.

---

## Insight 4 — Qué se pide en la 2ª visita  (F11, F12, F13, F13b)

Lista de productos **sobre-representados en la visita #2** vs. el promedio general, ordenados por esa
desproporción, cada uno con **señal de tamaño de muestra** (D6). Sin selector de N (F12: N=2 fijo).

```
┌── Card ─────────────────────────────────────────────────────────────────────┐
│  Qué se pide en la 2ª visita                                                  │
│  Productos que aparecen más en la segunda visita que en el promedio general.  │
│                                                                               │
│   PRODUCTO         SOBRE-REPRESENTACIÓN     RESPALDO                          │
│   Croissant        1.9× vs. promedio        en 24 de 60 segundas visitas      │
│   Cold brew        1.6× vs. promedio        en 19 de 60                       │
│   Muffin           1.4× vs. promedio        en 14 de 60                       │
│   …                                                                           │
│                                                                               │
│  Basado en 60 clientes con una 2ª visita en el rango.                         │  ← señal global de muestra
│  No incluye ventas anónimas (sin cliente no se puede secuenciar visitas).     │  ← nota al pie (F13b, D4)
└───────────────────────────────────────────────────────────────────────────────┘
```
- Cada fila: producto · **fuerza de sobre-representación** (p. ej. "1.9× vs. promedio" — forma exacta
  la fija tech, D6) · **respaldo** (en cuántas segundas visitas aparece). Ordenado por desproporción.
- **Nota al pie** de exclusión estructural de anónimas (F13b, D4): es información muted, **no** un
  error.
- **Estados:**
  - *muestra insuficiente* (pocos clientes con 2ª visita): estado propio, no conclusiones espurias:
    "Muestra insuficiente: solo N clientes tienen una 2ª visita en este rango. Amplía el rango para
    ver este insight." (criterio de aceptación Insight 4).
  - *loading* / *error* estándar.

---

## Insight 5 — Afinidad de canasta  (F14, F15, F16)

Top de **pares de productos** comprados juntos, con **fuerza de asociación** y **soporte** (nº de
ventas conjuntas). Pares bajo el umbral mínimo no se muestran (F16). Anónimas cuentan (nivel de
venta/línea).

```
┌── Card ─────────────────────────────────────────────────────────────────────┐
│  Afinidad de canasta                                                          │
│  Productos que se compran juntos más de lo que el azar explicaría.            │
│                                                                               │
│   PAR                          FUERZA           RESPALDO                       │
│   Latte  +  Croissant          Alta  (2.4×)     en 140 ventas juntas          │
│   Espresso  +  Cookie          Media (1.7×)     en 88 ventas                   │
│   Cold brew  +  Muffin         Media (1.5×)     en 61 ventas                   │
│   …                                                                           │
│                                                                               │
│  Solo se muestran pares con respaldo suficiente.                              │  ← umbral (F16), valor lo fija tech
└───────────────────────────────────────────────────────────────────────────────┘
```
- Cada fila: **par** (`A + B`) · **fuerza de asociación** (etiqueta + medida; método y forma los fija
  tech, T3/D6) · **respaldo** (nº de ventas conjuntas, F15).
- Umbral mínimo de soporte aplicado en query (F16); la UI solo lo enuncia. Valor concreto = tech con
  datos reales (P5).
- **Estados:**
  - *vacío / muestra insuficiente*: "Aún no hay pares de productos con suficiente respaldo en este
    rango." (no muestra pares anecdóticos).
  - *loading* / *error* estándar.

---

## Insight 6 — Efectividad de lealtad  (F17, F18, F19)

Comparación **dos columnas**: "Canjeó lealtad" vs. "No canjeó" (segmento = `sales.loyalty_reward IS
NOT NULL`, T5). Filas de comparación: ticket promedio, ventas/cliente (proxy de recurrencia), gasto/
cliente. Solo clientes identificados (F19).

```
┌── Card ─────────────────────────────────────────────────────────────────────┐
│  Efectividad de lealtad                                                       │
│  ¿El cliente que canjea una recompensa se comporta distinto?                  │
│                                                                               │
│                          CANJEÓ LEALTAD        NO CANJEÓ                       │
│   Ticket promedio          $172                 $131            ▲ +31%         │  ← contraste
│   Ventas por cliente       4.8                  2.1             ▲ +2.3×        │
│   Gasto por cliente        $820                 $275            ▲ +2.9×        │
│                                                                               │
│   Basado en 45 clientes que canjearon · 155 que no.                           │  ← respaldo por segmento
│   No incluye ventas anónimas (sin cliente no hay canje que atribuir).         │  ← nota al pie (F19, D4)
└───────────────────────────────────────────────────────────────────────────────┘
```
- Tres métricas × dos segmentos (F17/F18); columna de contraste (▲/▼ %) para leer de un golpe si la
  lealtad mueve la aguja. `tabular-nums`, dinero via `toPesos`.
- **Nota al pie** de exclusión estructural de anónimas (F19, D4): información muted, no error.
- **Estados:**
  - *segmento vacío* (sin canjes en el rango): se indica sin romperse — "Sin canjes de lealtad en
    este rango." en la columna "Canjeó", conservando la columna "No canjeó" (criterio de aceptación
    Insight 6).
  - *loading* / *error* estándar.

---

## Componentes reutilizados / nuevos

- **Reutilizados:** `Card`, `Button`, `Input`/`DateInput` (rango), `Select` (sucursal), patrón de
  header de página, barras proporcionales de Reportes (`bg-accent` sobre `bg-bg`), chips de %/conteo
  (`rounded-full bg-bg px-2 py-0.5 text-xs`), `Alert` (aviso "sin costo capturado"), tabla/lista de
  Reportes, `tabular-nums`, `toPesos`.
- **Nuevos (formalizar en design-system, §M9):**
  1. **Filtro rango + sucursal reutilizable** — extraer el selector de Reportes a un componente
     compartido con presets configurables (Insights usa 30/90/Personalizado; Reportes Hoy/Ayer/
     Personalizado). Lo exige el PRD ("mismo selector").
  2. **Tarjeta de insight** — `Card` con estructura: titular llano → detalle → estados por-tarjeta
     (carga/vacío/muestra insuficiente/error).
  3. **Stat / KPI block** — label muted + número grande `tabular-nums` + sub-línea de respaldo
     ("N ventas", "k/N"). Ya emerge en Reportes ("Total vendido"); se formaliza.
  4. **Ranking list con barra** — fila `rank · label · valor` + barra proporcional; generaliza el
     desglose por categoría de Reportes.
  5. **Comparación por segmentos** — 2–3 columnas de stat con fila de contraste (▲/▼ %). Usado en
     Insight 3 y 6.
  6. **Estado "muestra insuficiente"** — variante de estado vacío con copy específico (Insight 4/5).
  Detalle en `handoff.md` y en `foundations/design-system.md`.
</content>
