# Estado — Módulo convenio-discounts (M12)
_Actualizado: 2026-10-07_

## Etapa actual
🔨 Tech-spec ✅ (ADR-010) → Backend ✅ → Frontend ✅ → Review ✅ APROBADO → en curso: **demo local con Silvin** (commits faro 863d206 · faro-ui fbac172).

Ajuste del orquestador al spec (gate): caso "sin cliente + agreementDiscountId" se RECHAZA con 422 en vez de ignorarse en silencio (evita que el POS cobre con descuento y la venta se registre a precio completo — mismo patrón de pérdida silenciosa del incidente 2026-07-10).

## Pipeline (liviano: requisitos cerrados en el brief; diseño de UI acotado dentro del tech-spec)
- [x] Brief (decisiones de Silvin 2026-10-07: lealtad + convenio se suman; botones solo con %; toda la compra; todos los roles; catálogo dinámico)
- [x] Tech-spec + contratos (tech-lead) — `tech-spec.md`, `ADR-010`
- [x] Backend (backend-engineer) — migración 0031 (aplicada solo a faro_test), paquete agreementdiscounts, tx de venta, reportes; suite verde con `go test -p 1` (los paquetes de integración comparten faro_test: correr serializado)
- [x] Frontend (frontend-engineer) — POS (botones dinámicos, desglose, 422 visible), /agreement-discounts (Convenios, grupo Clientes), SaleTicket (Convenio + Cobró), reportes (tarjeta + columnas). Pendiente confirmar con Silvin: 'Reactivar' recrea el % (no flip de status).
- [x] QA de regresión del POS — cubierta por los tests de integración del backend (8 casos de venta); QA manual en la demo
- [x] Code review (code-reviewer) — APROBADO sin bloqueantes. Menores: reactivar=recrear (acumula filas inactive), preview con catálogo cacheado, convenio con monto 0 cuando lealtad cubre todo
- [ ] Demo local → confirmación de Silvin
- [ ] Deploy (migración 0031+ a Neon, Fly, Vercel)

## Ajuste agregado 2026-10-07 (mismo deploy): visitas previas solo admins + auditoría
Pedido de Silvin: cajero/barista no ven ni pueden mandar priorVisits al crear cliente (403 prior_visits_forbidden); super_admin/branch_admin sí. Migración 0032 (customers.created_by + customer_visit_changes, escrita en la misma tx en alta y PATCH visits), GET /customers/{id}/visit-changes (admins). Review APROBADO. Migración 0032 aplicada a faro_test y BD dev.
