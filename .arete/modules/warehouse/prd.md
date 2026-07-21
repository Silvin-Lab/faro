# PRD — Almacén (warehouse)
_Autor: project-manager · Fecha: 2026-07-20 · Estado: **aprobado** (Silvin, 2026-07-20) · Módulo: M8 · Depende de: supplies (existente), business-settings/sucursales_

## Problema
Faro maneja existencias **por sucursal** (`supply_branch_stock` + ledger `supply_movements`),
y hoy toda la operación —entrada de compra, ajuste/merma e historial— vive apilada dentro de la
pantalla de edición del insumo (`/supplies/[id]/edit`). No existe una **bodega central**
intermedia entre "comprar" y "la sucursal consume el insumo". Falta: control de **mínimos y
máximos** que avise cuándo reponer, un flujo de **compras** con **proveedores**, y un flujo de
**salidas** del almacén hacia las sucursales. La operación de abastecimiento no está
sistematizada y la navegación mezcla conceptos.

## Usuarios y casos de uso
- **Admin / dueño del negocio (super admin):** administra el almacén central, define mín/máx por
  insumo, registra compras, da de alta proveedores, y despacha salidas hacia las sucursales.
- **Encargado de compras / almacén:** consulta qué insumos están en o por debajo del mínimo,
  registra la compra al proveedor, y actualiza el stock del almacén.
- **Sucursal (receptora):** recibe la salida del almacén; ese ingreso queda **visible y con fecha**
  en el historial de inventario de su sucursal.
> **v1 sin roles** (consistente con el charter): cualquier usuario autenticado puede operar el
> módulo. La segmentación por rol es futura.

Casos de uso principales:
1. Ver el stock del almacén y detectar insumos que llegaron al mínimo → decidir comprar.
2. Registrar una compra (entrada al almacén) con proveedor, precio, cantidad y fecha.
3. Dar de alta / mantener el catálogo de proveedores.
4. Despachar una salida del almacén hacia una sucursal (o registrarla como merma).
5. Registrar una merma (en el almacén, o de producto que había salido a una sucursal).

## Objetivos y métricas de éxito
- El encargado ve de un vistazo **qué insumos hay que comprar** (en/bajo mínimo).
- Toda entrada y salida del almacén queda **auditada** (fecha, cantidad, contraparte).
- Una salida hacia una sucursal se refleja **automáticamente y de forma auditable** en el
  inventario de esa sucursal (sin doble captura manual).
- La navegación de inventario queda ordenada en una sección **Almacén** con submenús claros.

## Requisitos funcionales
### Stock de almacén con mínimos/máximos y alerta
- **F1** El almacén central es **único** por instancia (no hay uno por sucursal en este MVP).
- **F2** Los ítems del almacén son los **mismos insumos** del catálogo `supplies` (no un catálogo
  aparte). Cada insumo puede tener un **mínimo** y un **máximo** definidos **a nivel almacén**.
- **F3** El almacén lleva su **propio stock** por insumo (fuente de verdad: entradas − salidas),
  independiente del stock por sucursal.
- **F4** El sistema ofrece una **pantalla dedicada "Productos a comprar"** que lista **todos**
  los insumos cuyo stock de almacén está **en o por debajo de su mínimo**. Es una vista/pantalla
  propia (no un simple badge inline en la vista de almacén), pensada como la herramienta de
  reposición del encargado.

### Compras (entrada al almacén) + catálogo de proveedores
- **F5** Registrar una **compra** = entrada al almacén con: **fecha**, **proveedor**, **precio de
  compra por presentación** y **cantidad**. El precio se captura **por presentación** (bote,
  bolsa, etc. — la misma "presentación" que ya existe en el catálogo: `packageName` /
  `packageContent` / `packageCostCents`), **no** por unidad base. Suma al stock del almacén del
  insumo correspondiente.
- **F6** **Catálogo de proveedores** (entidad nueva): **nombre**, **dirección**, **correo
  electrónico** y **teléfono de contacto**. CRUD para poder seleccionar proveedor al comprar.
- **F7** Las compras quedan en un **historial auditable** (qué, cuánto, a quién, cuándo, a qué
  precio).

### Salidas (almacén → sucursal)
- **F8** Registrar una **salida** del almacén con: **cantidad**, **fecha** y **sucursal destino**.
  Resta del stock del almacén.
- **F9** Una salida con sucursal destino **suma automáticamente** al inventario de esa sucursal
  (`supply_branch_stock`).
- **F10** Ese ingreso a la sucursal debe quedar como un **movimiento/entrada visible con su
  fecha** en el **historial de inventario de la sucursal** (no un ajuste silencioso del cache;
  tiene que ser auditable desde la sucursal).

### Mermas (concepto único, sucursal opcional)
- **F11** La **merma** es un **concepto único** donde la **sucursal es opcional**:
  - (a) **Merma en el almacén central:** producto eliminado / caducado / echado a perder estando
    en el almacén → salida **sin** sucursal destino.
  - (b) **Merma asociada a una sucursal:** producto que **salió** del almacén hacia una sucursal y
    terminó desechado / no vendido (ej.: se enviaron 100 galletas, regresaron 10 → esas 10 entran
    como merma asociada a esa sucursal).
- **F12** Las mermas quedan en el **historial auditable** con su motivo/fecha y, si aplica, la
  sucursal asociada.
> Nota de negocio: esto **generaliza** el actual "Ajuste/merma" (hoy siempre atado a una sucursal)
> hacia un concepto con sucursal opcional. Si eso **reemplaza** o **coexiste** con la tabla actual
> es decisión de **tech-spec** (ver Riesgos). El PRD solo fija el comportamiento de negocio.

### Reorganización de navegación
- **F13** Crear una sección **"Almacén"** en el menú principal con submenús: **Compras**,
  **Mermas**, **Salidas**.
- **F14** Retirar de `/supplies/[id]/edit` **solo los formularios de escritura** de **"Entrada de
  compra"** y **"Ajuste/merma"** (esas altas se hacen ahora desde la sección Almacén). El
  **"Historial de movimientos" se mantiene en `/supplies/[id]/edit`, de solo lectura** (sin los
  forms de alta) — así el historial de inventario de la sucursal sigue visible ahí y **F10**
  (auditabilidad en la sucursal) queda satisfecho sin una pantalla nueva. Tras el ajuste, la
  edición del insumo queda enfocada en los datos del catálogo + la consulta del historial, no en
  el registro de existencias.

## Requisitos no funcionales
- **Auditabilidad:** toda entrada/salida/merma es un registro con fecha y contraparte; nada muta
  el stock sin dejar rastro.
- **Consistencia:** el stock mostrado del almacén = entradas − salidas; el reflejo en la sucursal
  no debe permitir doble contabilidad de la misma salida.
- **Coherencia visual:** respeta el design system existente de Faro (definición de UI → designer).

## Alcance
### En alcance (MVP)
- Almacén central único con mín/máx por insumo + alerta de reposición.
- Compras con catálogo de proveedores nuevo.
- Salidas almacén→sucursal con reflejo automático y auditable en el inventario de la sucursal.
- Mermas como concepto único con sucursal opcional.
- Reorganización de navegación a la sección Almacén (Compras / Mermas / Salidas) y limpieza de
  `/supplies/[id]/edit`.

### Fuera de alcance / futuro
- **Sugerencia de compra por histórico:** recomendar **dónde comprar** cada insumo según histórico
  de precio/proveedor. Futuro.
- **Mínimos/máximos por sucursal:** además del almacén central. Candidato futuro.
- **Multi-almacén / bodega por sucursal:** el MVP asume **un** almacén central.
- **Salida automática por venta:** el descuento de insumos al vender ya existe hoy pero no es parte
  del rediseño de este módulo (ver Riesgos R1).

## Criterios de aceptación
- [ ] Puedo definir mínimo y máximo (a nivel almacén) para un insumo del catálogo.
- [ ] El almacén muestra el stock por insumo.
- [ ] Existe una pantalla **"Productos a comprar"** que lista todos los insumos en o bajo su
      mínimo de almacén.
- [ ] Puedo dar de alta un proveedor (nombre, dirección, email, teléfono) y editarlo.
- [ ] Puedo registrar una compra (fecha, proveedor, **precio por presentación**, cantidad) y el
      stock del almacén sube la cantidad indicada; queda en el historial de compras.
- [ ] Puedo registrar una salida (cantidad, fecha, sucursal destino) y el stock del almacén baja.
- [ ] Tras esa salida, el inventario de la sucursal destino sube la cantidad y aparece como un
      **movimiento con fecha** en el historial de esa sucursal.
- [ ] Puedo registrar una merma **sin** sucursal (merma del almacén) y una merma **con** sucursal
      (producto que había salido y se desechó); ambas quedan auditadas.
- [ ] Existe una sección "Almacén" en el menú con submenús Compras, Mermas y Salidas.
- [ ] `/supplies/[id]/edit` ya no muestra los **formularios** de "Entrada de compra" ni
      "Ajuste/merma" (esas altas viven ahora en la sección Almacén); el **"Historial de
      movimientos" permanece ahí, de solo lectura**.

## Dependencias
- **supplies (existente, en prod):** catálogo `supplies`, `supply_branch_stock` (cache por
  sucursal), `supply_movements` (ledger firmado; tipos `purchase | adjustment | sale`).
- **business-settings (M7) / sucursales:** las salidas apuntan a una **sucursal destino**;
  requiere el catálogo de sucursales.
- **Proveedores:** entidad nueva creada dentro de este módulo.

## Riesgos y preguntas para tech-lead (entrada a tech-spec)
- **R1 — [RESUELTO, sin conflicto] Interacción con `deductSupplies` (descuento por venta):** el
  descuento automático de insumos al vender (`deductSupplies` en `internal/sales/store.go`) **ya
  existe en producción y NO choca** con las Salidas de almacén: son **flujos complementarios**
  sobre el **mismo** `supply_branch_stock`, en direcciones opuestas — una **Salida SUMA** al
  inventario de la sucursal (entra), una **venta lo RESTA** vía `deductSupplies` (sale). No hay
  doble contabilidad de la misma operación. Aclaración de Silvin: hoy el descuento por venta no se
  observa en la práctica solo porque **aún no configuró recetas** de producto en producción (el
  motor existe, no está en uso activo). El tech-lead debe respetar esta convivencia, no mitigar un
  conflicto inexistente.
- **R2 — Modelo de datos del almacén (nuevo) vs. reutilización de `supply_movements`:** Silvin
  confirmó que el almacén es una **entidad nueva** (no sucursal virtual), pero la mecánica de
  "salida almacén→sucursal que además genera un movimiento auditable en la sucursal" toca el
  ledger existente. Definir en tech-spec si la pata de sucursal se modela como un `supply_movement`
  (¿nuevo tipo, p.ej. `transfer`?) y cómo se evita la doble contabilidad.
- **R3 — Mermas: reemplazar vs. coexistir con `type=adjustment`:** el negocio pide un concepto de
  merma único con sucursal opcional. Decidir si **absorbe/reemplaza** el actual "Ajuste/merma"
  (siempre atado a sucursal) o **coexisten**, y qué pasa con los movimientos `adjustment`
  históricos. Migración de datos a evaluar.
- **R4 — Stock negativo / conciliación:** el ledger actual permite stock negativo. Definir el
  comportamiento del stock de almacén (¿permite negativo?, ¿bloquea salida sin existencias?,
  ¿solo avisa?).

## Preguntas abiertas (producto) — RESUELTAS (Silvin, 2026-07-20)
- **[RESUELTA]** ~~¿La alerta de reposición es solo visual o requiere pantalla/notificación?~~ →
  **Pantalla dedicada "Productos a comprar"** que lista los insumos en o bajo su mínimo (ver F4),
  no un badge inline. Notificaciones (WhatsApp/email) quedan fuera de alcance de este MVP.
- **[RESUELTA]** ~~¿El precio de compra se captura por presentación o por unidad base?~~ →
  **Por presentación** (bote, bolsa, etc.; misma "presentación" del catálogo `supplies`), no por
  unidad base (ver F5).
