# Research / Decisiones de UX — Almacén (warehouse)
_Autor: product-designer · Fecha: 2026-07-20 · Módulo: M8 · Fuente: prd.md (aprobado)_

> No hubo research primario nuevo (usuarios internos ya conocidos, dueño de producto ya
> alineado en el PRD). Este documento captura los **jobs-to-be-done** y las **decisiones de UX**
> que gobiernan los wireframes y el handoff, para que ingeniería entienda el _por qué_.

## Jobs-to-be-done
1. **"Cuando reviso el almacén, quiero ver de un vistazo qué insumos hay que comprar, para no
   quedarme sin stock ni sobre-comprar."** → pantalla dedicada _Productos a comprar_ (F4) +
   banner de reposición en la landing.
2. **"Cuando llega una compra, quiero registrarla rápido (fecha, proveedor, precio por
   presentación, cantidad) y que el stock suba solo."** → formulario de Compras con cálculo en
   vivo de unidades base que entran (F5, F7).
3. **"Cuando despacho a una sucursal, quiero que el inventario de esa sucursal se actualice sin
   volver a capturar."** → formulario de Salidas con reflejo automático y auditable (F8–F10).
4. **"Cuando se echa a perder producto, quiero registrarlo, esté en el almacén o ya en una
   sucursal."** → Mermas con sucursal opcional (F11, F12).
5. **"Quiero mantener mis proveedores para elegirlos al comprar."** → CRUD de Proveedores (F6).

## Decisiones de UX (con rationale)

### D1 — Dónde vive "Productos a comprar" en la navegación
**El PRD (F13) nombra solo 3 submenús (Compras, Mermas, Salidas) y no ubica _Productos a comprar_
(F4).** Decisión de diseño:
- **Ítem de menú propio** dentro de la sección **Almacén**, en **2.ª posición** (bajo la landing
  _Almacén_, sobre _Compras_).
- Reforzado con un **banner de reposición** en la landing (`N insumos en o bajo su mínimo →`) y
  un **atajo desde Compras**.

Rationale: F4 lo describe como _"una vista/pantalla propia… la herramienta de reposición del
encargado"_, no un badge inline. Enterrarlo dentro de Compras lo escondería del job #1, que es el
punto de entrada mental del encargado ("¿qué compro hoy?"). El orden del menú refleja el flujo
real: **ver qué falta → comprar**. Se documenta explícitamente como decisión del diseñador porque
el PRD no la resolvió.

### D2 — Dónde se configura mín/máx por insumo
Mín/máx son un concepto **de almacén**, no del catálogo. F14 exige que la edición del insumo quede
enfocada en datos de catálogo (nombre, unidad, presentación, categoría, medidas) y **libre de
manejo de existencias**. Por eso mín/máx se editan **inline en la landing de Almacén**
(_Existencias del almacén_), no en `/supplies/[id]/edit`. Así el mismo lugar donde se ve el stock
del almacén es donde se define el umbral que dispara la alerta.

### D3 — Merma con sucursal opcional (un solo formulario)
En vez de dos pantallas (merma de almacén / merma de sucursal), **un formulario único** con el
campo _Sucursal_ opcional cuyo valor por defecto es **"Sin sucursal (almacén central)"**. Esto
materializa el "concepto único" de F11 y evita bifurcar la UI. El texto de ayuda explica los dos
casos.

### D4 — Roles: sin gate por rol (v1)
El PRD (charter) dice **v1 sin roles**: cualquier usuario autenticado opera el módulo. Las
pantallas actuales de `supplies` sí están _gated_ a `super_admin`. Para el módulo Almacén **no se
replica ese gate**: las páginas requieren solo sesión válida. En el sidebar, sin embargo, la
sección Almacén se agrupa junto a la administración de inventario existente. Ver nota abierta en el
handoff (§Roles) para que PM/tech confirmen la ubicación exacta del ítem en cada perfil de menú.

### D5 — Formulario + historial en la misma pantalla
Compras, Salidas y Mermas presentan **formulario (card superior) + historial (card inferior)** en
una sola ruta, replicando el patrón apilado que hoy ya usa `/supplies/[id]/edit`. Minimiza
navegación y da feedback inmediato: registro → aparece en el historial de abajo. Proveedores sí usa
el patrón lista + página `/new` y `/[id]/edit` (es un CRUD de entidad, como Categorías de insumo).
</content>
</invoke>
