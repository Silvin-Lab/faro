# PRD — Repostería / Producción central (bakery)
_Autor: project-manager · Fecha: 2026-08-13 · Estado: **aprobado** (Silvin, 2026-08-13) · Módulo: M10 · Depende de: warehouse/supplies (existente), product-management, sales/POS, business-settings/sucursales, insights, auth/roles_

## Problema
El grupo opera una **repostería central** que produce postres y **surte a todas las sucursales**,
que los venden como productos normales. Hoy el proceso es **100% informal**: la sucursal avisa por
fuera del sistema qué le falta y la repostería surte a ojo. No hay **trazabilidad** de qué se
produjo/entregó, la repostería **no se entera en automático** por las ventas de qué postres hay que
reponer, y **no se puede proyectar** qué postre reforzar cuando sube la demanda. Este módulo
sistematiza ese flujo con un **pedido trazable de sucursal → producción → acreditación de stock**,
y da **visibilidad de la tendencia de venta** de postres para apoyar la decisión de qué producir.

## Usuarios y casos de uso
- **Sucursal (branch_admin / cashier / barista):** registra **pedidos de postres** a la repostería,
  consulta el histórico de sus pedidos, ve el **stock de postres** que le fue acreditado, y puede
  marcar un pedido como **recibido** (informativo). Solo opera sobre **su propia** sucursal.
- **Repostero (rol nuevo):** login propio, **limitado al módulo de producción**. Ve la **cola de
  pedidos de todas las sucursales**, **registra la producción** contra un pedido (total o parcial),
  consulta el **stock de insumos** del almacén central para saber si puede producir, y ve la
  **tendencia de venta** de postres. **No pertenece a ninguna sucursal** y no ve el resto de la
  administración.
- **Super admin / dueño del negocio:** **visibilidad total** del módulo (todos los pedidos,
  producciones, stock por sucursal y tendencia). Puede **operar en ausencia de un repostero
  asignado** (registrar producción, gestionar la cola), define qué productos son "de repostería" y
  administra el alta del usuario repostero.

Casos de uso principales:
1. Una sucursal pide N unidades de un postre a la repostería.
2. La repostería revisa la cola de pedidos pendientes de todas las sucursales y decide qué producir.
3. La repostería registra la producción contra un pedido (posiblemente en parcialidades) y con ello
   se descuentan insumos y se acredita el stock del postre a la sucursal.
4. Una sucursal cancela un pedido que aún no ha entrado en producción.
5. Una sucursal marca como recibido un pedido que ya le fue despachado.
6. Una sucursal (o super admin) consulta el stock de postres terminados por sucursal.
7. Cualquiera con acceso (repostero / super admin / branch_admin en su sucursal) consulta la
   tendencia de venta de postres (semana actual vs. anterior) para decidir qué reforzar.

## Objetivos y métricas de éxito
Objetivos:
- **Reemplazar el aviso informal** por un pedido registrado y trazable de punta a punta.
- **Trazabilidad completa:** todo lo que se produce y se entrega queda auditado (qué postre, cuánto,
  para qué sucursal, cuándo, por quién).
- **Cerrar el loop de reposición:** la venta de postres en la sucursal es visible para la repostería
  como tendencia, y el stock producido se descuenta al vender.
- **Apoyar la decisión de producción** con datos de demanda reciente, sin depender de la intuición.

Métricas de éxito (criterio cualitativo donde no hay línea base numérica real):
- **% de pedidos con trazabilidad completa** (pedido → producción → despacho registrados en el
  sistema): meta cualitativa = **la totalidad** de los surtidos pasa por el flujo, dejando de existir
  el surtido "a ojo" por fuera del sistema.
- **Reducción de quiebres de stock de postre** reportados informalmente por las sucursales (medido
  cualitativamente contra el estado actual, dado que hoy no existe registro).
- **Adopción del rol repostero:** la producción se registra desde el módulo, no de memoria.
- **Uso de la tendencia:** la vista de tendencia se consulta como parte de la decisión semanal de
  producción (señal cualitativa de que reemplazó la intuición).
> No se fijan porcentajes numéricos de negocio porque **no hay línea base**: hoy el proceso es
> informal y sin registro. Las metas se revisarán con datos reales una vez el módulo esté en uso.

## Requisitos funcionales

### Marcar un producto como "de repostería"
- **F1** El super admin puede indicar que un producto del catálogo es **surtido por la repostería
  central** (vs. preparado en la propia sucursal). Solo los productos marcados como de repostería
  participan del flujo de pedidos, producción y stock de postre de este módulo.
- **F2** Para un producto de repostería, la **receta** (insumos que consume) se interpreta como el
  **consumo por unidad producida** en la central; ese consumo descuenta del **almacén central
  compartido**, no del inventario de la sucursal.

### Crear pedido desde la sucursal
- **F3** Un usuario de sucursal (branch_admin / cashier / barista) puede **crear un pedido** a la
  repostería con: **producto (postre)**, **cantidad** y **nota opcional**. El pedido queda dirigido
  a **su** sucursal automáticamente (no elige otra sucursal).
- **F4** Al crearse, el pedido nace en estado **`pending`** y aparece en el **histórico de pedidos**
  de la sucursal y en la **cola de la repostería**.
- **Criterios de aceptación:**
  - [ ] Puedo crear un pedido eligiendo un postre y una cantidad, con nota opcional.
  - [ ] El pedido queda asociado a mi sucursal y en estado `pending`.
  - [ ] Solo puedo pedir productos que son de repostería.

### Ver la cola de pedidos en la repostería
- **F5** El repostero y el super admin ven una **cola con los pedidos de todas las sucursales**, con
  al menos: sucursal, postre, cantidad pedida, cantidad ya despachada, estado, nota y fecha.
- **F6** La cola permite **filtrar** (p. ej. por estado y/o sucursal) para trabajar los pendientes
  primero. Un usuario de sucursal, en cambio, **solo ve sus propios pedidos**.
- **Criterios de aceptación:**
  - [ ] Como repostero veo los pedidos de todas las sucursales, no solo de una.
  - [ ] Puedo distinguir los pendientes de los ya despachados / recibidos / cancelados.
  - [ ] Como usuario de sucursal veo únicamente los pedidos de mi sucursal.

### Registrar producción contra un pedido (incluye producción parcial)
- **F7** El repostero (o el super admin) puede **registrar una producción** contra un pedido:
  indica la **cantidad producida** para ese pedido. Solo es posible si el pedido está en
  **`pending`** o **`in_production`**.
- **F8 (parcialidad):** la cantidad producida puede ser **menor** que la pedida. En ese caso el
  pedido pasa (o permanece) en **`in_production`** y refleja la **cantidad acumulada despachada**.
  Se pueden registrar **varias producciones** contra el mismo pedido hasta completarlo.
- **F9 (cierre):** cuando la cantidad acumulada despachada **alcanza o supera** la cantidad pedida,
  el pedido pasa a **`shipped`**.
- **F10 (doble efecto al producir):** registrar una producción, en un solo acto:
  - **descuenta los insumos** de la receta del **almacén central compartido**, y
  - **acredita el stock del postre a la sucursal** del pedido (entrega directa, sin tránsito).
  Ambos efectos y la producción quedan **auditados** (qué, cuánto, para qué sucursal, cuándo, quién).
- **F11** La acreditación de stock a la sucursal ocurre **en el momento de registrar la producción**;
  no hay estado intermedio de traslado.
- **Criterios de aceptación:**
  - [ ] Puedo registrar una producción indicando cuántas unidades produje para un pedido.
  - [ ] Si produzco menos de lo pedido, el pedido queda `in_production` con la cantidad despachada
        acumulada, y puedo seguir produciendo contra él después.
  - [ ] Cuando lo despachado alcanza lo pedido, el pedido pasa a `shipped`.
  - [ ] Al registrar la producción, el stock de insumos del almacén central baja según la receta y
        el stock del postre de la sucursal sube en la cantidad producida.
  - [ ] La producción queda registrada de forma auditable (postre, cantidad, sucursal, fecha, autor).
  - [ ] No puedo registrar producción contra un pedido `cancelled`, `shipped` (ya completo) ni
        `received`.

### Cancelar un pedido
- **F12** Un usuario de la sucursal dueña del pedido (o el super admin) puede **cancelar** un pedido
  **solo si está en `pending`** y **nada se ha producido aún**. Un pedido con producción registrada
  (parcial o total) **no se puede cancelar**.
- **F13** Un pedido cancelado pasa a **`cancelled`** y sale de la cola de trabajo de la repostería;
  queda en el histórico para trazabilidad.
- **Criterios de aceptación:**
  - [ ] Puedo cancelar un pedido en `pending` que no tiene producción.
  - [ ] No puedo cancelar un pedido que ya está `in_production`, `shipped` o `received`.

### Marcar como recibido (informativo)
- **F14** La sucursal dueña de un pedido puede marcarlo como **`received`** cuando ya fue despachado
  (`shipped`). Este estado es **puramente informativo**: confirma la recepción física, **no mueve
  stock** (el stock ya se acreditó al producir).
- **Criterios de aceptación:**
  - [ ] Puedo marcar como recibido un pedido que está `shipped`.
  - [ ] Marcar recibido no cambia el stock de postre de la sucursal (ya estaba acreditado).

### Ver stock de postres por sucursal
- **F15** Existe una vista de **stock de postres terminados por sucursal**. La sucursal ve **su**
  stock; el repostero y el super admin ven el de **todas** las sucursales.
- **F16** La **venta** de un postre en la sucursal (vía POS) **descuenta** ese stock de postre
  terminado; un carrito mixto (café + postre) descuenta correctamente cada cosa de donde
  corresponde (el postre de su stock terminado, sin re-descontar insumos que ya se consumieron al
  producir).
- **Criterios de aceptación:**
  - [ ] Como sucursal veo el stock de postres que tengo disponible.
  - [ ] Como repostero / super admin veo el stock de postres de todas las sucursales.
  - [ ] Vender un postre en la sucursal reduce su stock de postre terminado.

### Ver stock de insumos (repostero)
- **F17** El repostero puede **consultar el stock de insumos del almacén central compartido** para
  saber si tiene material para producir. Es **solo lectura**: el repostero **no** gestiona compras,
  proveedores, salidas ni mermas del almacén (eso sigue siendo exclusivo del super admin).
- **Criterios de aceptación:**
  - [ ] Como repostero puedo ver el stock de insumos del almacén central.
  - [ ] Como repostero NO puedo registrar compras, salidas, mermas ni gestionar proveedores.

### Ver tendencia de venta de postres (extensión de Insights)
- **F18** Existe una vista de **tendencia de venta de postres** que compara **la semana actual vs. la
  anterior**, por **postre** y por **sucursal**, para apoyar la decisión de qué producir más. Es
  **agregación histórica simple (SQL), sin IA** ni predicción automática.
- **F19** Acceso a la tendencia: **repostero** (todas las sucursales), **super admin** (todas, con
  filtro), **branch_admin** (su sucursal). Cashier/barista no acceden a esta vista.
- **Criterios de aceptación:**
  - [ ] Puedo ver, por postre, cuánto se vendió esta semana vs. la anterior.
  - [ ] Como repostero / super admin puedo verlo para todas las sucursales; como branch_admin, solo
        la mía.
  - [ ] La vista no promete predicción: solo muestra el histórico comparado.

### Rol repostero
- **F20** Existe un **rol nuevo `repostero`** con login propio. Su acceso está **limitado al módulo
  de producción**: cola de pedidos, registrar producción, stock de postres, stock de insumos (solo
  lectura) y tendencia de venta. **No ve** el resto de la administración.
- **F21** El repostero **no pertenece a ninguna sucursal** y **no se le puede asignar** una. El alta
  del usuario repostero la hace el super admin.
- **Criterios de aceptación:**
  - [ ] Puedo crear un usuario con rol repostero (sin sucursal).
  - [ ] Al iniciar sesión como repostero aterno directo en su módulo de producción, sin selección de
        sucursal.
  - [ ] No es posible asignarle una sucursal a un repostero (ni al crearlo ni al editarlo).
  - [ ] Un repostero no puede acceder a módulos fuera de producción (p. ej. reportes, ventas,
        configuración).

## Requisitos no funcionales
- **Auditabilidad:** cada pedido, producción, cambio de estado y movimiento de stock (insumo y
  postre) queda registrado con fecha, cantidad y autor; nada muta el stock sin dejar rastro.
- **Consistencia:** el stock de postre de una sucursal y el de insumos del almacén son coherentes con
  la suma de sus movimientos; una misma producción no debe contabilizarse dos veces ni permitir que
  el descuento de venta re-descuente insumos ya consumidos al producir.
- **Concurrencia:** dos producciones registradas casi al mismo tiempo contra el mismo pedido no deben
  corromper la cantidad despachada ni el estado del pedido.
- **Seguridad / gating por rol:** cada acción respeta el rol (sucursal solo lo suyo; repostero solo
  producción; super admin todo). El acceso de lectura de insumos para el repostero no debe abrir el
  resto del almacén a otros roles.
- **Coherencia visual:** respeta el design system existente de Faro (definición de UI → designer).

## Alcance

### En alcance (MVP)
- Marcar productos como "de repostería" y su receta como consumo por unidad producida.
- Pedido de sucursal a la repostería con histórico.
- Cola de pedidos para repostero / super admin, con producción total y parcial.
- Doble efecto al producir: descuento de insumos del almacén central + acreditación de stock del
  postre a la sucursal (sin tránsito).
- Ciclo de estados `pending → in_production → shipped → received` + `cancelled` (solo desde
  `pending`, sin producción).
- Stock de postres por sucursal y descuento al vender en POS.
- Consulta de stock de insumos (solo lectura) para el repostero.
- Tendencia de venta de postres (Insights), semana actual vs. anterior, sin IA.
- Rol `repostero` con login propio, sin sucursal.

### Fuera de alcance / futuro (documentado para no asumirlo implícito)
- **Almacén separado para repostería:** el MVP usa el **almacén central único** compartido.
- **Paso de "en tránsito" / logística con transportista:** no hay estado de traslado ni demora;
  producir = acreditar a la sucursal.
- **Proyección automática / IA de demanda:** solo **tendencia histórica simple**; sin predicción.
- **Reasignar el rol de repostero a una sucursal:** el repostero nunca tiene sucursal.
- **Push automático de producción por ventas:** el disparo es el **pedido explícito** de la
  sucursal; el sistema no genera pedidos ni produce por sí mismo.

## Criterios de aceptación (resumen del MVP)
- [ ] El super admin puede marcar un producto como "de repostería".
- [ ] Una sucursal puede crear un pedido (postre, cantidad, nota) que nace en `pending`.
- [ ] El repostero ve la cola de pedidos de todas las sucursales y puede filtrarla.
- [ ] El repostero puede registrar producción parcial y total; el estado del pedido avanza
      correctamente (`in_production` → `shipped`).
- [ ] Al producir, bajan los insumos del almacén central y sube el stock del postre en la sucursal,
      todo auditado.
- [ ] Se puede cancelar un pedido solo en `pending` y sin producción previa.
- [ ] La sucursal puede marcar `received` (informativo, sin mover stock).
- [ ] Existe la vista de stock de postres por sucursal y la venta en POS lo descuenta.
- [ ] El repostero puede ver (solo lectura) el stock de insumos del almacén central.
- [ ] Existe la vista de tendencia de venta de postres (semana actual vs. anterior) con el gating de
      roles definido.
- [ ] Existe el rol `repostero` con login propio, sin sucursal, limitado al módulo de producción.

## Dependencias
- **warehouse / supplies (existente, en prod):** consumo de insumos de producción sobre el **almacén
  central único** compartido; lectura de stock de insumos para el repostero.
- **product-management:** marcar productos como de repostería y su receta.
- **sales / POS:** fuente de la tendencia de venta y descuento del stock de postre terminado al
  vender.
- **business-settings / sucursales:** pedidos y acreditación de stock apuntan a una sucursal.
- **insights (existente):** la tendencia de venta de postres es una extensión de este módulo.
- **auth / roles:** alta y gating del rol `repostero`; ajuste de visibilidad de lectura del almacén.

## Riesgos y preguntas para tech-lead (entrada a tech-spec)
> Existe un **diseño técnico detallado ya aprobado** por el dueño (modelo de datos, endpoints,
> transacción de producción, migraciones). El tech-lead lo retomará en la tech-spec; este PRD solo
> fija el comportamiento de negocio. Los puntos de atención de negocio a validar técnicamente:
- **R1 — Doble semántica de la receta según tipo de producto:** para un postre de repostería la
  receta se consume **al producir en la central**, no al vender en la sucursal. La venta de un postre
  **no** debe re-descontar insumos (ya se consumieron). Evitar doble contabilidad es crítico.
- **R2 — Stock de producto terminado (nuevo):** hoy los productos no tienen stock propio por
  sucursal; este módulo lo introduce para los postres. Definir cómo convive con el flujo de venta
  actual y qué pasa con productos no-repostería (que siguen sin stock propio).
- **R3 — Concurrencia en el registro de producción:** producciones simultáneas contra el mismo pedido
  no deben corromper la cantidad despachada ni el estado. Requiere serialización a nivel de pedido.
- **R4 — Invariante "repostero sin sucursal":** el punto frágil es la **edición** de usuarios; hay
  que blindar que no se le pueda asignar una sucursal a un repostero por ningún camino.
- **R5 — Apertura de lectura del almacén al repostero:** el módulo de almacén hoy es exclusivo del
  super admin; abrir **solo** la lectura de stock de insumos al repostero sin exponer compras /
  proveedores / salidas / mermas a otros roles requiere regresión de autorización.
- **R6 — Stock negativo / conciliación:** el ledger actual permite stock negativo. Definir el
  comportamiento al producir sin insumos suficientes (¿bloquea?, ¿permite negativo?, ¿solo avisa?),
  consistente con el criterio ya usado en `warehouse`/`supplies`.
