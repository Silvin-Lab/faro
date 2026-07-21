# Brief — Almacén (warehouse)

_Fecha: 2026-07-20 · Estado: ✅ alcance claro (gate cerrado; ver PRD) · Depende de: supplies (existente), business-settings/sucursales_

## Problema
Hoy el manejo de existencias vive **por sucursal** dentro de la pantalla de edición del
insumo (`/supplies/[id]/edit`): "Entrada de compra", "Ajuste/merma" e "Historial de
movimientos" mezclados, sobre `supply_branch_stock` + el ledger `supply_movements`
(`internal/supplies`). No existe una **bodega central** intermedia entre "comprar" y "la
sucursal consume el insumo", ni control de **mínimos/máximos** que avise cuándo reponer, ni
catálogo de **proveedores** para registrar compras. La operación de abastecimiento no está
sistematizada.

## Alcance MVP
1. **Stock de almacén central (único).** Una bodega central por instancia (no una por
   sucursal en este MVP). Cada ítem/insumo de almacén tiene **mínimo** y **máximo**
   definidos. El stock del almacén se lleva como fuente de verdad propia (entradas − salidas).
2. **Alerta de reposición.** Cuando el stock de un ítem cae **al mínimo o por debajo**, el
   sistema avisa que hay que comprar.
3. **Compras (entrada al almacén).** Registrar una entrada con **fecha, proveedor, precio de
   compra y cantidad**. Suma al stock del almacén.
4. **Catálogo de proveedores (nuevo).** Entidad nueva con **nombre, dirección, correo
   electrónico y teléfono de contacto**. CRUD para poder seleccionar el proveedor al registrar
   una compra.
5. **Salidas (almacén → sucursal).** Mover stock **del almacén** hacia una **sucursal destino**
   específica: se captura **cantidad, fecha y sucursal**. Resta del stock del almacén.
   Confirmado como deseable por Silvin (pero sin decidir el mecanismo, ver preguntas abiertas):
   que una Salida **sume automáticamente** al `supply_branch_stock` de la sucursal destino.
6. **Reorganización de navegación (en alcance).** Sacar de `/supplies/[id]/edit` las secciones
   "Entrada de compra", "Ajuste/merma" e "Historial de movimientos", y moverlas a una sección
   nueva **"Almacén"** en el menú principal, con submenús: **Compras**, **Mermas**, **Salidas**.

## Fuera de alcance / futuro
- **Sugerencia de compra por histórico.** El sistema recomienda **dónde comprar** cada insumo
  según histórico de precio/proveedor. Explícitamente futuro, no MVP.
- **Mínimos/máximos por sucursal.** Además del almacén central, mín/máx a nivel de cada
  sucursal. Candidato a extensión futura; anotado para no perderlo.
- **Múltiples almacenes / bodega por sucursal.** El MVP asume **un** almacén central; la
  posibilidad de varios almacenes queda para después.

## Preguntas abiertas (resueltas por Silvin 2026-07-20 — gate cerrado)
1. **[RESUELTA] Modelo del almacén central.** ~~¿Entidad de datos nueva propia, o "sucursal
   virtual" reutilizando `supply_branch_stock` + `supply_movements`?~~ → **Entidad de datos
   NUEVA** (no sucursal virtual). Confirmado explícitamente.
2. **[RESUELTA] Salida → stock de sucursal.** ~~¿Suma automática o sistemas independientes?~~ →
   **SÍ suma automáticamente** al `supply_branch_stock` de la sucursal destino. Además, la Salida
   debe quedar como **movimiento/entrada visible con su fecha** en el historial de esa sucursal
   (no un ajuste silencioso al cache). _Riesgo a reconciliar con tech-lead: ya existe descuento
   automático de insumos al vender (`deductSupplies` en `internal/sales/store.go`); ver
   riesgos del PRD._
3. **[RESUELTA] Mermas.** ~~¿Nuevo submenú vs. ajuste por sucursal actual?~~ → **Concepto único**
   con **sucursal opcional**: (a) merma en almacén central = Salida SIN sucursal destino;
   (b) merma asociada a sucursal = producto que salió a una sucursal y terminó desechado/no
   vendido (ej. 100 galletas enviadas, 10 regresan como merma). No son dos sistemas paralelos.
   Sugiere generalizar/reemplazar el "Ajuste/merma" actual (`type=adjustment`, siempre atado a
   sucursal); decisión de modelo de datos → tech-spec.
4. **[RESUELTA] Ítems de almacén = catálogo de insumos.** ~~¿Mismos `supplies` o catálogo
   aparte?~~ → **Los mismos `supplies`**. Mínimo/máximo son atributos **nuevos a nivel almacén**
   (no a nivel sucursal — eso sigue fuera de alcance/futuro).

## Dependencias
- **supplies (existente, en prod):** `supply_branch_stock` (cache por sucursal) y
  `supply_movements` (ledger firmado; tipos `purchase | adjustment | sale`). El brief NO propone
  modificarlo; la relación exacta (reutilizar vs. entidad nueva) es pregunta abierta P1/P4.
- **business-settings (M7) / sucursales:** las Salidas apuntan a una **sucursal destino**;
  requiere el catálogo de sucursales.
- **Proveedores:** entidad nueva a crear dentro de este módulo (no existe hoy).

## Gate
**Alcance claro ✅** — cerrado el 2026-07-20 con las respuestas de Silvin a P1–P4.
Siguiente artefacto: `prd.md` (ya escrito). Luego diseño (`product-designer`) y tech-spec
(`tech-lead`).
