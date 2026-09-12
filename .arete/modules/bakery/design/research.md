# Research / Decisiones de UX — Repostería / Producción central (bakery)
_Autor: product-designer · Fecha: 2026-08-13 · Módulo: M10 · Fuente: prd.md (aprobado) · brief.md · diseño técnico aprobado (Feature 2)_

> No hubo research primario nuevo (usuarios internos ya conocidos, dueño de producto ya alineado en
> el PRD y en un diseño técnico aprobado). Este documento captura los **jobs-to-be-done** y las
> **decisiones de UX** que gobiernan los wireframes y el handoff, para que ingeniería entienda el
> _por qué_. El modelo de datos y los endpoints ya están definidos en el diseño técnico; aquí solo
> se diseña la experiencia sobre ese modelo.

## Jobs-to-be-done
1. **Sucursal — "Cuando me falta un postre, quiero pedirlo a la repostería y que quede registrado,
   para dejar de avisar por fuera del sistema."** → pantalla de pedido (postre + cantidad + nota) con
   histórico y estado visible (F3, F4).
2. **Sucursal — "Quiero saber cuánto postre tengo disponible para vender, sin preguntarle a nadie."**
   → vista de stock de postres de mi sucursal (F15).
3. **Repostero — "Cuando llego a producir, quiero ver de un vistazo qué me piden todas las
   sucursales y qué falta por surtir, para decidir qué hacer primero."** → cola de producción con
   "falta por surtir" explícito y antigüedad del pedido (F5, F6, F8).
4. **Repostero — "Cuando termino una tanda (aunque sea parcial), quiero registrarla una sola vez y
   que baje el insumo y suba el stock de la sucursal, sin doble captura."** → modal "Registrar
   producción" con preview del doble efecto (F7–F11).
5. **Repostero — "Antes de comprometerme a producir, quiero ver si tengo insumos."** → lectura del
   almacén central en solo lectura (F17).
6. **Repostero / dueño — "Quiero ver qué postre subió de demanda esta semana para reforzarlo."** →
   tendencia semana actual vs. anterior, ordenada por variación (F18, F19).
7. **Super admin — "Quiero marcar qué productos surte la repostería y que su receta se entienda como
   consumo al producir, no al vender."** → selector de tipo de producto + relabel de la receta
   (F1, F2).

## Decisiones de UX (con rationale)

### D1 — El pedido de sucursal solo ofrece postres "de repostería"
El selector de producto del pedido (Pantalla 1) lista **únicamente** productos con
`fulfillment_type='bakery'` (criterio de aceptación de F3: "Solo puedo pedir productos que son de
repostería"). Evita que la sucursal pida un producto que se prepara localmente y que no tiene flujo
de producción. Si el catálogo aún no tiene postres marcados, se muestra un estado vacío que remite al
administrador (no un select vacío mudo).

### D2 — Cómo se comunica la parcialidad: columna "Surtido n/m + barra" y "Falta" explícito
La parcialidad (F8) es el concepto central del módulo y el punto donde el proceso informal fallaba
("surtió a ojo"). En vez de solo mostrar el estado, cada pedido lleva:
- **Surtido**: `cantidad_despachada / cantidad_pedida` (ej. `3 / 10`) con una **barra de progreso**
  proporcional (mismo lenguaje que la _Ranking list con barra_ de Insights).
- **Falta**: `pedida − despachada` en número grande/énfasis, porque es lo accionable para el
  repostero ("¿cuánto me falta hacer?").
Así un pedido `in_production` no es un estado opaco: se ve exactamente cuánto lleva y cuánto resta.

### D3 — "Registrar producción" es un modal desde la fila, con preview del doble efecto
El diseño técnico define una transacción con **doble efecto** (baja insumos del almacén central +
acredita stock del postre a la sucursal, F10). Para que el repostero **confíe** en que un solo acto
hace las dos cosas, el modal muestra un **preview en vivo**:
- "Consumirá del almacén central: {receta × cantidad}."
- "Acreditará {cantidad} u. de {postre} a «{sucursal}»."
- El estado resultante ("Quedará **Surtido**" o "Quedará **En producción**, faltarán {n}").
Se resuelve como **modal** (no pantalla aparte) porque la acción es contextual a un pedido concreto
de la cola y debe volver a la cola sin perder el filtro/scroll. Es el mismo patrón mental que la
Salida del almacén ("resta de aquí, suma allá"), que ya generó confianza en M8.

### D4 — Indicador de antigüedad (aging) para priorizar la cola
El PRD pide "trabajar los pendientes primero" (F6) y menciona pedidos que "llevan días sin
atenderse". La cola ordena por **más antiguo primero** (FIFO) y marca con un indicador sutil
(reloj + "N días" en tono _atención_) los pedidos `pending`/`in_production` con **≥ 2 días** sin
cerrarse. No es un error (no usa `danger` fuerte): es una señal de prioridad. Umbral de 2 días
elegido por criterio propio (postre = producto perecedero de reposición frecuente); es fácil de
ajustar y se documenta como decisión del diseñador porque el PRD no fijó el número.

### D5 — Insumos para el repostero: reuso de la landing de Almacén en solo lectura, sin acción de compra
F17 es **solo lectura**. Se reutiliza la tabla de _Existencias del almacén_ (M8) pero **retirando**
todo lo de escritura: sin edición inline de mín/máx, sin "Ajustar existencia", sin enlaces a
Compras/Salidas/Mermas. El banner de bajo-mínimo se conserva como **información** (para que el
repostero sepa si hay material), pero **sin** el botón "Ver qué comprar →" (el repostero no compra).
Así se cumple F17 sin abrir el resto del almacén (consistente con el gating técnico: solo
`GET /warehouse/stock`).

### D6 — Tendencia: un bloque reutilizable, ordenado por variación en unidades
La tendencia (F18/F19) se diseña como **un bloque de contenido reutilizable** que se embebe en dos
lugares según el rol:
- **/bakery/trend** — página propia del **repostero** (no ve el módulo Insights completo, que es de
  comportamiento de cliente y está gated a super_admin/branch_admin).
- **Insights** — como sección/tarjeta para **super_admin** (con filtro de sucursal) y **branch_admin**
  (acotado a su sucursal por el servidor).
Se ordena por **mayor variación en unidades** (no por % ni alfabético) porque la decisión real es
"qué postre reforzar": el que **más creció en volumen** encabeza. Cada fila muestra ▲/▼ con la
diferencia, en el lenguaje de _Comparación por segmentos_ del DS. Alcance por sucursal: el filtro
gobierna el scope ("Todas" agrega por postre; elegir una sucursal muestra por postre de esa
sucursal), satisfaciendo "por postre y por sucursal" de F18 sin una tabla cruzada densa.

### D7 — `fulfillment_type` en el form de producto + relabel de la receta (mismo editor)
El tipo de producto (F1) se captura en el **mismo formulario de edición de producto** (solo
super_admin), no en una pantalla nueva, porque es un atributo del producto. Al marcarlo como "de
repostería", el editor de **Receta** existente **no cambia de estructura**: solo se relabela su
subtítulo de "consume al **vender**" a "consume al **producir** en la central" (F2). Reusar el mismo
editor evita duplicar UI y mantiene una sola forma de capturar recetas. El selector solo lo ve
super_admin (F1).

### D8 — Estado del pedido: StatusBadge con 5 variantes (extiende el DS con `success`)
El pedido tiene 5 estados. Se reutiliza el `StatusBadge` de M8 pero requiere una variante más:
- `pending` → **muted** ("Pendiente")
- `in_production` → **accent/lime** ("En producción" — color de trabajo en curso)
- `shipped` → **success/verde** ("Surtido")
- `received` → **success + ✓** ("Recibido" — terminal, confirmado por la sucursal)
- `cancelled` → **danger/tinte** ("Cancelado")
`success` es una **extensión del DS** (hoy `StatusBadge` solo tiene accent/danger/muted); se
formaliza en `foundations/design-system.md`.

### D9 — Ubicación en el sidebar por rol
- **Repostero** (rol nuevo, sin sucursal): grupo **plano** (sin encabezados, como los roles de
  sucursal), landing directa en `/bakery/queue`: _Cola de producción · Insumos · Tendencia de venta_.
- **Super admin**: nuevo grupo **"Repostería"** (_Cola de producción · Stock de postres_); la
  tendencia vive dentro de su Insights. Puede operar la cola en ausencia del repostero (PRD).
- **Roles de sucursal** (branch_admin / cashier / barista): dos ítems nuevos en su menú plano:
  _Pedidos a repostería · Stock de postres_. La tendencia, solo para branch_admin, vive en su
  Insights (no ítem aparte).

### D10 — Stock insuficiente al producir: aviso suave, no bloqueante
El ledger de almacén **permite stock negativo** (criterio ya vigente en warehouse/supplies, R6 del
PRD). Al registrar producción con insumos insuficientes, el modal muestra un **aviso `warning` no
bloqueante** ("El almacén no tiene suficiente {insumo}; se registrará y quedará en negativo") y
permite continuar. Si tech decidiera **bloquear**, la misma UI lo soporta mostrando el aviso como
error inline y deshabilitando el botón. Decisión final = tech-spec; el diseño soporta ambas.
</content>
</invoke>
