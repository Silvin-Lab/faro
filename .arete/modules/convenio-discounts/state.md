# Estado — Módulo convenio-discounts (M12)
_Actualizado: 2026-10-07_

## Etapa actual
🔨 Tech-spec ✅ (ADR-010) → Backend ✅ → Frontend ✅ → en curso: **code review**.

Ajuste del orquestador al spec (gate): caso "sin cliente + agreementDiscountId" se RECHAZA con 422 en vez de ignorarse en silencio (evita que el POS cobre con descuento y la venta se registre a precio completo — mismo patrón de pérdida silenciosa del incidente 2026-07-10).

## Pipeline (liviano: requisitos cerrados en el brief; diseño de UI acotado dentro del tech-spec)
- [x] Brief (decisiones de Silvin 2026-10-07: lealtad + convenio se suman; botones solo con %; toda la compra; todos los roles; catálogo dinámico)
- [x] Tech-spec + contratos (tech-lead) — `tech-spec.md`, `ADR-010`
- [x] Backend (backend-engineer) — migración 0031 (aplicada solo a faro_test), paquete agreementdiscounts, tx de venta, reportes; suite verde con `go test -p 1` (los paquetes de integración comparten faro_test: correr serializado)
- [x] Frontend (frontend-engineer) — POS (botones dinámicos, desglose, 422 visible), /agreement-discounts (Convenios, grupo Clientes), SaleTicket (Convenio + Cobró), reportes (tarjeta + columnas). Pendiente confirmar con Silvin: 'Reactivar' recrea el % (no flip de status).
- [ ] QA de regresión del POS (qa-engineer)
- [ ] Code review (code-reviewer)
- [ ] Demo local → confirmación de Silvin
- [ ] Deploy (migración 0031+ a Neon, Fly, Vercel)
