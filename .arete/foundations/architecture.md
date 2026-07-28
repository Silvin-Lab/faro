# Arquitectura — Faro
_Fecha: 2026-06-29 · Nace con: módulo login (M1) · Se extiende con cada módulo_

## Visión general
Aplicación web **multi-negocio (multi-tenant)** para administración de cafeterías + POS.

```
[ Next.js (admin + POS web) ]  ──HTTPS/JSON──►  [ Go API ]  ──►  [ PostgreSQL ]
        TypeScript, Tailwind                     net/http+Chi, pgx        (Neon en prod)
```

## Vista de componentes
| Componente | Tecnología | Responsabilidad | Dueño |
|------------|-----------|-----------------|-------|
| Web app | React + Next.js (App Router, TS, Tailwind) | UI de administración y POS; consume la API | frontend-engineer |
| API | Go (Chi router, pgx) | Lógica de negocio, auth, contratos REST | backend-engineer |
| Base de datos | PostgreSQL (Neon en prod) | Persistencia con aislamiento por negocio | backend-engineer |
| Plataforma | Docker + GitHub Actions + Fly.io | Build, deploy, runtime | devops-engineer |

## Estructura de repos y despliegue (decisión base — ver ADR-004, reemplaza ADR-003)
**Dos repositorios separados desde el MVP**, comunicados por **HTTP (REST/JSON)**:
- **`faro`** (este repo) = **backend** Go, monolito modular (código en la **raíz**). Aloja las specs `.arete/`.
- **`faro-ui`** = **frontend** Next.js.

```
faro/  (backend)                     faro-ui/  (frontend)
├── cmd/api/                         ├── app/            # Next.js App Router
├── internal/<modulo>/  auth,…       ├── lib/api         # cliente HTTP del backend
├── migrations/                      ├── Dockerfile
├── Dockerfile                       └── .env.local      # NEXT_PUBLIC_API_URL
├── docker-compose.yml  (db + api)
└── .arete/  (specs del producto)

   faro-ui  ──HTTP/JSON (cookie de sesión httpOnly)──►  faro (API)
```

- **Comunicación:** el frontend consume la API por HTTP. El backend habilita **CORS** para el origen del frontend con `AllowCredentials` (la sesión viaja en cookie httpOnly).
- **Cookies cross-origin:** en prod, back y front bajo el **mismo sitio registrable** (ej. `api.faro.app` / `app.faro.app`) para que `SameSite=Lax` funcione; si quedaran en sitios distintos, la cookie debe ser `SameSite=None; Secure` (definir en `runbook.md` al desplegar).
- **Monolito modular** en el backend: módulos internos (`auth`, `products`, `sales`, `loyalty`), no microservicios. CI/CD y despliegue **independientes** por repo.
> El **contrato de API es la frontera** entre los dos repos; se versiona y ninguno importa código del otro.

## Multi-tenancy (decisión base — ver ADR-001)
- **Modelo:** base de datos compartida, esquema compartido, columna **`tenant_id`** en las tablas con datos de negocio.
- **Aislamiento:** la API acota **toda** consulta al `tenant_id` del usuario autenticado. El **super admin global** (`tenant_id` nulo, `is_super_admin`) puede operar sobre cualquier negocio de forma explícita.
- **Endurecimiento futuro:** Row-Level Security (RLS) de Postgres como segunda barrera.

## Autenticación (decisión base — ver ADR-002)
- **Login:** email + password. Password hasheado con **bcrypt**.
- **Sesión:** **JWT (HS256)** firmado, guardado en cookie **httpOnly + Secure + SameSite=Lax**; expiración ~8 h (un turno), renovación deslizante.
- **Middleware:** valida la sesión, carga el usuario y su `tenant_id`, y acota el scope. (PIN y cajas: fase posterior.)

## Negocio único, sucursales y personalización (M7 — ver ADR-006 + ADR-007)
- **Negocio único:** el sistema opera **un solo `tenants`**. El **super admin** (dueño
  global, `tenant_id NULL`) lo administra vía `businessTenantID()`; se retira `POST /tenants`.
  Se conserva el esquema multi-tenant por mínimo cambio.
- **Sucursales (`branches`)** son una **capa organizativa** bajo `tenants`; **no** subdividen
  el aislamiento (clientes, lealtad, productos siguen compartidos). Administración (branches,
  favicon, usuarios) **solo super admin** (`RequireSuperAdmin`).
- **Membresía M:N** (`user_branches`): el personal pertenece a 1+ sucursales. Tras login
  elige su **sucursal activa**, que viaja como **claim en el JWT** (`POST /auth/select-branch`
  la fija, re-emitiendo la cookie). `POST /sales` deriva `sales.branch_id` del claim
  (server-trusted; no se confía en el cliente). POS: `Faro. {sucursal activa}`.
- **Favicon por negocio:** columna `tenants.favicon_url` (reusa `uploads`/`/files/*`),
  inyectado en runtime en el cliente (no `generateMetadata`, por ser app autenticada
  cross-origin). Expuesto vía `GET /auth/me`/login extendidos (`tenant{}`, `branches`,
  `activeBranchId`).

## Almacén central (M8 — ver ADR-008)
- **Almacén único por negocio** (`tenant`), intermedio entre "comprar" y "la sucursal
  consume el insumo". Es una **entidad de datos nueva** (`warehouse_stock` +
  `warehouse_movements`), **no** una sucursal virtual. Sus ítems son los mismos `supplies`.
- **Dos dominios de stock separados**, cada uno con su ledger firmado y su invariante
  `cache == SUM(ledger)`: almacén (`warehouse_stock`/`warehouse_movements`, tipos
  `purchase | dispatch | waste`) y sucursal (`supply_branch_stock`/`supply_movements`,
  existente).
- **La salida (dispatch) cruza ambos dominios** en una transacción: resta del almacén y
  suma a la sucursal destino como un `supply_movement` tipo **`transfer`** (nuevo, positivo,
  ligado por FK `warehouse_movement_id`). Complementa —no colisiona con— el descuento por
  venta (`deductSupplies`, que resta). Detalle en ADR-008 y `modules/warehouse/tech-spec.md`.
- **Módulo** `internal/warehouse` montado en `/warehouse`, gated a **super_admin**.

## Insights de negocio (M9 — ver ADR-009)
- **Módulo `internal/insights`** montado en `/insights`, **solo lectura**, mismo patrón
  store/service que `internal/reports`. Gating idéntico a reports: super_admin (branchId libre)
  + branch_admin (acotado a su sucursal); cashier/barista → 403.
- **Determinístico, sin IA:** 6 insights (recurrencia, producto estrella, ticket por segmento,
  2ª visita, afinidad de canasta, efectividad de lealtad) calculados con SQL/Go. **Ninguna ruta
  llama a un LLM** (decisión de negocio cerrada).
- **Cálculo en vivo, sin cache/precómputo** (ADR-009): a volumen single-tenant es lo más simple
  y lo más barato en Neon (CU-hora). Señal de revisión documentada en la tech-spec.
- **Sin esquema nuevo:** agrega sobre `sales`, `sale_items`, `products`, `product_supplies`,
  `supplies`, `loyalty_redemptions`. El canje de lealtad se lee de **`loyalty_redemptions`**
  (la columna `sales.loyalty_reward` fue eliminada en 0010; ver ADR-009). Detalle en
  `modules/insights/tech-spec.md`.

## Límites y contratos
- **Web ↔ API:** REST/JSON. La cookie de sesión viaja en cada request (no hay tokens en localStorage).
- **API ↔ DB:** acceso solo desde la API; el frontend nunca habla con la DB.
- Contrato detallado por módulo en `modules/<m>/tech-spec.md`.

## Requisitos no funcionales y cómo se cumplen
- **Seguridad:** hashing de contraseñas, cookie httpOnly/Secure, rate limiting en login, aislamiento por tenant.
- **Rendimiento:** índices por `tenant_id` y por claves de búsqueda; consultas acotadas.
- **Despliegue:** contenedores Docker en Fly.io; Postgres gestionado en Neon (ver `runbook.md`).

## Riesgos
- Fuga entre tenants si una consulta olvida el `tenant_id` → mitigar con un helper de acceso a datos que **exija** tenant scope + (futuro) RLS.
- Secreto de firma del JWT debe estar en gestión de secretos (no en repo).
