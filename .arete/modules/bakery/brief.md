# Brief — Repostería / Producción central (bakery)

_Fecha: 2026-08-13 · Estado: ✅ alcance claro (gate cerrado; ver PRD) · Depende de: warehouse/supplies (existente), business-settings/sucursales, product-management, sales, insights, auth/roles_

## Problema
El grupo opera una **repostería central** que produce postres y **surte a todas las
sucursales**, que a su vez los venden como productos normales. Hoy ese proceso es **100%
informal**: la sucursal avisa **por fuera del sistema** qué le falta y la repostería surte
**a ojo**. Consecuencias:

- **No hay trazabilidad** de qué se produjo ni de qué se entregó a cada sucursal.
- La repostería **no se entera en automático** —por las ventas de la sucursal— de qué postres
  hay que reponer.
- **No se puede proyectar** qué postre conviene reforzar cuando sube la demanda, porque no hay
  visibilidad de la tendencia de venta por postre y sucursal.

En palabras del dueño (Silvin): _"el proceso es que la sucursal comenta lo que falta y la
repostería le surte pero no hay control automático, no se entera en automático por las ventas de
la sucursal los postres que se tienen que surtir ni podemos proyectar qué postre hacer más esta
semana porque subió la demanda"._

## Alcance MVP
1. **Flujo "Pedido de sucursal".** Cada sucursal **registra un pedido** (qué postre, qué
   cantidad, nota opcional) dirigido a la repostería central. Sustituye el aviso informal por un
   registro trazable.
2. **Cola de pedidos en repostería.** La repostería **ve la cola** de pedidos pendientes de
   **todas las sucursales**, produce y **despacha/surte contra esos pedidos**. Se permiten
   **entregas parciales** contra un mismo pedido.
3. **Insumos: almacén central COMPARTIDO.** Los insumos de repostería (harina, azúcar, etc.) se
   agregan al **mismo catálogo de insumos y al mismo almacén único** ya existente (módulo
   `warehouse`). **No** se crea un segundo almacén.
4. **Acreditación de stock al producir.** El stock del postre **se acredita a la sucursal en el
   momento en que la repostería registra la producción**. No hay paso de "en tránsito": la
   entrega es directa/manual, sin transportista con demora.
5. **Rol nuevo "repostero".** Login propio en Faro, **limitado al módulo de producción/pedidos**,
   sin ver el resto de la administración. **No pertenece a ninguna sucursal.**
6. **Visibilidad de tendencia de venta de postres** (semana actual vs. anterior) por sucursal,
   como **extensión del módulo Insights** existente, para ayudar a decidir qué producir más.
   Agregación SQL pura, **sin IA**.
7. **Ciclo de vida del pedido.** Estados: `pending → in_production → shipped → received`, más
   `cancelled`. La cancelación solo es posible desde `pending` y solo si **nada se ha producido
   aún**. El estado `received` (lo marca la sucursal) es **informativo**: no mueve stock.

## Fuera de alcance / futuro
- **Almacén separado para repostería.** El MVP usa el **almacén central único** compartido; no hay
  bodega propia de la repostería.
- **Paso de "en tránsito" / logística con transportista.** No hay estado intermedio de traslado ni
  demora de entrega: producir = acreditar a la sucursal.
- **Proyección automática / IA de demanda.** El MVP solo muestra **tendencia histórica simple**
  (semana actual vs. anterior). Cualquier predicción automática es futura.
- **Reasignar el rol de repostero a una sucursal.** El repostero **nunca** pertenece a una
  sucursal; no se contempla asignarle una.
- **Push automático de producción por ventas.** El disparo sigue siendo el **pedido explícito** de
  la sucursal; el sistema no genera pedidos ni produce solo por caída de stock (la tendencia de
  venta es un apoyo a la decisión, no un automatismo).

## Decisiones tomadas con el dueño (gate cerrado 2026-08-13)
Todas las decisiones de alcance de arriba fueron **confirmadas con Silvin** antes de escribir este
brief; no son preguntas abiertas. Se formalizan como requisitos en el PRD:
- Flujo de pedido de sucursal (no push automático). ✅
- Almacén central compartido (no un segundo almacén). ✅
- Rol `repostero` con login propio y sin sucursal. ✅
- Tendencia de venta de postres dentro de Insights, sin IA. ✅
- Stock acreditado a la sucursal al producir; sin tránsito. ✅
- Estados del pedido y reglas de cancelación. ✅

## Dependencias
- **warehouse / supplies (existente, en prod):** el consumo de insumos de la producción descuenta
  del **almacén central único** compartido; el repostero necesita **ver el stock de insumos** de
  ese almacén.
- **product-management:** los postres son **productos del catálogo**; se necesita distinguir cuáles
  son "de repostería" (surtidos por la central) de los que la sucursal prepara localmente.
- **sales / POS:** las ventas de postres en la sucursal son la fuente de la **tendencia de venta**
  y deben descontar el stock de postre terminado que la producción acreditó.
- **business-settings / sucursales:** el pedido y la acreditación de stock apuntan a una
  **sucursal**.
- **insights (existente):** la vista de tendencia de venta de postres es una **extensión** de este
  módulo.
- **auth / roles:** alta del rol nuevo `repostero` y su gating de acceso.

## Gate
**Alcance claro ✅** — cerrado el 2026-08-13 con las decisiones de Silvin listadas arriba.
Siguiente artefacto: `prd.md` (ya escrito). Luego diseño (`product-designer`) y tech-spec
(`tech-lead`). Existe además un **diseño técnico detallado ya aprobado** que el tech-lead retomará
en la fase de tech-spec (fuera de este brief, que habla en términos de negocio/producto).
