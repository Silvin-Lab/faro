# Módulo: convenio-discounts — Brief
_Creado: 2026-10-07 · Estado: 🔍 discovery (alcance cerrado con Silvin) · Depende de: pos (M4), loyalty (M6), sales-reports (M5), business-settings/roles_
_Rama: `feat/convenio-discounts` desde `prod` (faro 03983cf · faro-ui 85816c6). Migraciones desde **0031** (0021–0030 reservadas por M10/M11 en otras ramas)._

## Qué resuelve
Faro tiene convenios con negocios y escuelas vecinas: sus integrantes reciben un % de descuento en su consumo.
Hoy no hay forma de otorgarlo en el punto de venta ni de auditar quién lo dio y cuánto costó.

## Resultado esperado
- En el POS, con un cliente asociado a la venta, el cajero toca un botón de % y el descuento se aplica al total. El servidor hace el cálculo.
- La venta queda registrada con el porcentaje, el monto del descuento y **el usuario que la cobró**.
- El detalle de la venta y los reportes muestran los descuentos otorgados (cantidad, por porcentaje, monto).

## Alcance
- **En:**
  1. **Catálogo de descuentos de convenio (admin, super admin):** CRUD de porcentajes (valor 1..100, activo/inactivo). Seed inicial: **10%** y **15%**. El POS muestra **un botón por cada descuento activo**, ordenados por %; el botón muestra **solo el porcentaje**.
  2. **POS:** los botones están **deshabilitados** mientras la venta no tenga cliente asociado y se habilitan al asociarlo. Se puede seleccionar y quitar. Un solo descuento de convenio por venta. Lo otorga **cualquier rol** (cajero, barista, branch_admin, super_admin).
  3. **Aplica sobre toda la compra.**
  4. **Convive con lealtad (se suman):** primero la promo de lealtad existente (descuento de una unidad), luego el % de convenio sobre el subtotal restante. Cálculo **siempre en servidor**; el cliente solo manda el id del descuento.
  5. **Registro en la venta:** snapshot del % y monto del convenio, separado del descuento de lealtad (`discount_cents` hoy es solo lealtad; no mezclar sin estrategia).
  6. **Usuario de la venta (nuevo, aplica a TODA venta):** `sales` no guarda quién cobró. Agregar el usuario de la sesión a cada venta nueva; las históricas quedan null ("sin registro").
  7. **Detalle de venta** (POS/ticket y historial de ventas en reportes): mostrar si tuvo descuento de convenio, %, monto y quién cobró.
  8. **Reportes:** por día/periodo, los descuentos de convenio otorgados: cantidad de ventas, desglose por porcentaje y monto total. Indicar si el total de ventas es neto (recomendado: neto, consistente con lealtad hoy).
- **Fuera:** nombre del convenio en el botón o ligar el cliente a un convenio (futuro); excluir productos o categorías; autorización por supervisor; descuentos en monto fijo.

## Reutiliza de foundations
- Patrón CRUD admin + lectura desde POS de `loyalty_promotions` (M6, ADR-005).
- Transacción de venta en `internal/sales/store.go` (cálculo en servidor de lealtad + `deductSupplies`).
- Sesión/roles (`auth.UserFromContext`), historial de ventas `GET /reports/sales/list`, `/reports/sales`.

## Notas / riesgos
- **Toca la transacción de venta del POS**, lo más usado en producción. Regresión obligatoria: venta normal, venta con promo de lealtad, venta con lealtad + convenio, 4 métodos de pago (cash/card/transfer/didi; los no-cash cobran exacto) y totales de reportes.
- Redondeo: definir en tech-spec (centavos, half-up) y que el ticket cuadre al centavo.
- Tests de integración: `TEST_DATABASE_URL` **debe** apuntar a `faro_test` (incidente 2026-08-06).
- Validar en demo local con Silvin antes de prod. Al desplegar: este módulo sale **antes** que M10/M11; la migración 0031 se aplica sobre una base que llega hasta 0020.
