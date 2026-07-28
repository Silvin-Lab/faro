# Brief — Insights (insights)

_Fecha: 2026-07-27 · Estado: 🔍 discovery (preguntas abiertas) · Depende de: sales-reports, loyalty, supplies (costo de insumos)_

## Problema
Faro tiene reportes de ventas y gastos (`internal/reports`), pero no responde preguntas de
**comportamiento** que Silvin necesita para tomar decisiones: qué % de clientes vuelve, cuál es
el producto estrella (y bajo qué definición), y si hay patrones de compra ligados al ciclo de
visitas del cliente (ej. qué se pide en la 2ª visita). Hoy esas respuestas no existen en la
plataforma; para obtenerlas habría que analizar la base de datos a mano.

## Decisión previa (Silvin, 2026-07-24/27)
- **Sin LLM en esta fase.** Todo el cálculo es agregación determinística (SQL/Go), sin llamadas
  a la API de Anthropic ni a ningún modelo — es un gasto que Silvin decidió no asumir todavía.
  Ver [[orchestrator-delegates-only]] para contexto de por qué esto se documenta como decisión
  explícita del negocio, no técnica.
- Fase futura (fuera de este módulo): capa de narración en lenguaje natural sobre las métricas
  ya calculadas — explícitamente pospuesta, no descartada.

## Alcance propuesto v1 (a confirmar con Silvin — ver preguntas abiertas)
1. **Recurrencia de clientes**: % de clientes con ≥2 ventas en el período / total con ≥1 venta
   en el período. Idealmente también por cohorte (nuevos en el período, % que vuelve en
   30/60/90 días).
2. **Producto estrella**: ranking por ingresos, por volumen (unidades) y por margen real
   (usa costo de receta de `internal/supplies` cuando esté capturado; si `packageCostCents` es
   null para el insumo, el producto queda fuera del ranking de margen, no se asume costo 0).
3. **Ticket promedio**: cliente nuevo vs. recurrente, en el período.
4. **Correlación visita-N ↔ producto**: usando `sales.customer_id` + `created_at` para numerar
   las visitas de cada cliente (`ROW_NUMBER() OVER (PARTITION BY customer_id ORDER BY
   created_at)`), qué productos aparecen desproporcionadamente en la visita #2 (u otra N)
   respecto al promedio general.
5. **Afinidad de canasta**: pares de productos que aparecen juntos en el mismo `sale_id` más de
   lo que el azar explicaría (co-ocurrencia simple, no necesariamente lift/confidence completo
   — el tech-lead decide el método según volumen de datos real).
6. **Efectividad de lealtad**: comparar gasto/recurrencia de clientes con `loyalty_reward`
   canjeado vs. los que no.

## Fuera de alcance / futuro
- Cualquier narración o chat sobre los datos vía LLM (ver decisión previa).
- Insights predictivos (forecasting, churn prediction con ML) — esto es descriptivo, no
  predictivo.
- Comparación entre negocios/tenants (Faro es single-tenant hoy).

## Preguntas resueltas (Silvin, 2026-07-27)
1. **[RESUELTA] Prioridad v1.** Los 6 insights entran completos en v1 — mismo estilo de query
   sobre las mismas tablas, sin razón técnica fuerte para partirlo en fases.
2. **[RESUELTA] Ubicación en navegación.** Sección nueva **"Insights"** en el sidebar, separada
   de "Reportes" (es comportamiento, no cifras operativas del día a día).
3. **[RESUELTA] Filtros.** Mismo selector de rango de fechas y sucursal que ya usa
   `sales-reports` (`internal/reports`) — consistencia con el resto de la plataforma.

## Pendiente para tech-spec (no bloquea el gate, lo resuelve tech-lead/backend-engineer)
4. **Gating de acceso**: se asume `super_admin` únicamente, igual que el resto de reportes
   admin — confirmar que no aplique a otro rol al momento de implementar.
5. **Volumen de datos real de Vanta** (ventas/clientes en prod hoy): el tech-lead debe
   verificarlo por query directa para decidir si alcanza con cálculo en vivo (como hace
   `reports` hoy) o si algún insight pesado (afinidad de canasta, cohortes) necesita
   pre-cómputo/cache.

## Gate
**Alcance claro ✅** — cerrado el 2026-07-27 con las respuestas de Silvin a P1–P3. Siguiente
artefacto: `prd.md` (project-manager).
