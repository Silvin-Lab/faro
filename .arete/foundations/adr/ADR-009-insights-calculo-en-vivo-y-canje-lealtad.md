# ADR-009 — Insights: cálculo en vivo determinístico y fuente real del canje de lealtad

_Fecha: 2026-07-27 · Estado: **aceptado** · Módulo: M9 insights · Depende de: 0010_loyalty_promotions (loyalty_redemptions), 0015/0016 (supplies/costo), internal/reports (patrón de scope), ADR-005 (lealtad v2)_

## Contexto

M9 (Insights) agrega 6 métricas de comportamiento (recurrencia, producto estrella, ticket
nuevo vs. recurrente, correlación 2ª visita, afinidad de canasta, efectividad de lealtad)
calculadas de forma **determinística, sin LLM** (decisión de negocio ya cerrada), sobre las
tablas existentes de ventas, insumos y lealtad. Es un módulo **solo lectura**.

Dos decisiones técnicas relevantes y reversibles-con-costo quedaron pendientes del PRD (T2 y
T5) y se resuelven aquí:

1. **T2 — En vivo vs. precómputo.** El PRD no fija arquitectura de cache. La afinidad de canasta
   (self-join sobre líneas de venta) y las cohortes son los candidatos a pesar. Prod corre en
   **Neon (plan Launch, cobro por CU-hora activa)** y el negocio es una cafetería única
   (single-tenant). No hay credenciales de Neon prod en el entorno de diseño para medir volumen.

2. **T5 — Fuente del "canje de lealtad" (Insight 6).** El PRD y el handoff de diseño asumen que
   el canje se lee de **`sales.loyalty_reward IS NOT NULL`** (valores `null | 'discount' | 'free'`).
   Al verificar el esquema real: esa columna la agregó `0009_loyalty` y **la eliminó
   `0010_loyalty_promotions` (línea 92)** cuando la lealtad pasó de config única a CRUD de
   promociones (ADR-005). El canje real de la lealtad v2 vive en la tabla **`loyalty_redemptions`**
   (una fila por recompensa canjeada, con `sale_id`, `customer_id`, `tenant_id`, snapshot de la
   promoción y `created_at`). La premisa del PRD/handoff es incorrecta contra el esquema vivo.

## Decisión

### D1 — Cálculo en vivo por request, sin cache ni precómputo (T2)

Insights calcula cada métrica con una consulta SQL directa por request, igual que
`internal/reports`. **No** se introduce precómputo, vista materializada ni cache en v1.

Fundamento:
- **Costo Neon (CU-hora):** el compute solo se consume cuando Silvin abre Insights; un cron de
  precómputo *añadiría* compute base recurrente. A volumen de Vanta las queries son de
  milisegundos → **la opción en vivo es también la más barata**, no solo la más simple.
- **Simplicidad sostenible:** sin invalidación, sin drift cache↔fuente, sin job programado.
- **Determinismo:** mismos filtros ⇒ mismo resultado, sin estado intermedio.

**Señal explícita de revisión** (no se actúa antes): p95 de cualquier `/insights/*` > 1.5 s
sostenido, **o** filas en `sales` del tenant > ~200 000, **o** `/insights/basket-affinity` > 2 s.
Primera medida: un índice `sales(tenant_id, customer_id, created_at)` (diferido). Solo si
persiste, evaluar vista materializada en un ADR nuevo.

### D2 — Insight 6 se calcula desde `loyalty_redemptions`, no desde `sales.loyalty_reward` (T5)

El segmento "canjeó lealtad" se define como **cliente con ≥1 fila en `loyalty_redemptions`**
atribuida a alguna de sus ventas del período (`EXISTS` por `sale_id`, agregado a nivel cliente
con `bool_or`). La columna `sales.loyalty_reward` **no se usa** (no existe en prod).

Esto **corrige** el T5 del PRD y §3.6 del handoff: el resultado de negocio (comparar gasto y
recurrencia de quien canjeó vs. quien no) es idéntico al pretendido; solo cambia la fuente de
datos por la real. Es un hecho del esquema, no un cambio de alcance.

### D3 — Sin migración de esquema; índice opcional diferido

Insights es solo lectura sobre tablas existentes. No crea tablas ni columnas. Un único índice
aditivo (`0020_sales_customer_idx`) queda **escrito pero no aplicado**, reservado para la señal
de D1.

## Consecuencias

**Positivas**
- Módulo minimalista (store solo `SELECT`), sin superficie de escritura ni estado que mantener.
- Costo de compute proporcional al uso real; nada corre en background.
- Insight 6 se apoya en la fuente de verdad real de la lealtad v2 (`loyalty_redemptions`),
  consistente con ADR-005.

**Negativas / riesgos**
- La afinidad (self-join) podría degradarse a volúmenes altos; mitigado por soporte mínimo
  (`HAVING`), naturaleza ~lineal en nº de ventas (k productos/venta pequeño) y la señal de D1.
- **Dependencia de que `0010_loyalty_promotions` esté aplicada en Neon prod.** Si no lo está,
  `loyalty_redemptions` no existe y Insight 6 falla (aislado por tarjeta, no tumba la página).
  Acción devops: confirmar la migración antes de exponer Insight 6.
- Corrige documentos aprobados (PRD/handoff): la implementación debe seguir este ADR, no la
  letra de T5.

## Alternativas consideradas

- **Precómputo/materialized view desde v1:** descartado — añade compute base y complejidad de
  invalidación sin necesidad al volumen actual; peor en costo Neon.
- **Reintroducir `sales.loyalty_reward`:** descartado — duplicaría el estado del canje que ya
  vive (correctamente y con más detalle) en `loyalty_redemptions`; sería deuda y fuente de
  divergencia.
- **Afinidad con confidence o Apriori:** descartado — confidence es direccional (incómodo para
  pares simétricos) y Apriori multi-ítem excede el alcance del PRD (v1 = pares). Se usa
  co-ocurrencia (soporte) + lift (simétrico, normalizado, barato).
