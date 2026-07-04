# Modelo ER — Faro (hasta migración 0012)

**Negocio único (ADR-007):** el sistema opera **un solo `tenants`** = el negocio. El super
admin (dueño global, `tenant_id NULL`, `is_super_admin`) lo administra vía
`businessTenantID()`; no pertenece a ninguna sucursal. Se conserva el esquema multi-tenant
(`tenant_id`) por mínimo cambio. Clientes y lealtad se comparten entre sucursales.

> **Sucursales (migraciones 0011 + 0012, ver ADR-006/ADR-007):** entra `branches` (capa
> organizativa) y `tenants` gana `favicon_url` (0011); `sales` gana `branch_id`. La sucursal
> del usuario es **M:N** vía `user_branches` (0012, reemplaza el `users.branch_id` único).
> El personal pertenece a 1+ sucursales; tras login elige su **sucursal activa** (claim en el
> JWT, `POST /auth/select-branch`), que etiqueta `sales.branch_id` (derivado en servidor) y el
> encabezado del POS (`Faro. {sucursal activa}`). Bucket "Sin sucursal" = `branch_id NULL`
> → reportes por sucursal. Administración de branches/favicon/usuarios: **solo super admin**.

> **v2 lealtad (migración 0010, ver ADR-005):** se pasa de config única a **promociones**.
> Se eliminan `loyalty_configs`, `loyalty_discount_products`, `loyalty_free_products`;
> entran `loyalty_promotions`, `loyalty_promotion_products`, `loyalty_redemptions`.
> `customers` gana `visits_lifetime`; `sales` pierde `loyalty_reward`.

```mermaid
erDiagram
    tenants ||--o{ users        : "emplea"
    tenants ||--o{ branches     : "opera"
    users    ||--o{ user_branches : "pertenece"
    branches ||--o{ user_branches : "incluye"
    branches ||--o{ sales       : "registra en"
    tenants ||--o{ categories   : "tiene"
    tenants ||--o{ products     : "tiene"
    tenants ||--o{ sales        : "registra"
    tenants ||--o{ customers    : "tiene"
    tenants ||--o{ loyalty_promotions : "define"

    categories ||--o{ products  : "agrupa"

    sales ||--o{ sale_items     : "compone"
    products ||--o{ sale_items  : "snapshot"
    customers ||--o{ sales      : "asociada a"

    loyalty_promotions ||--o{ loyalty_promotion_products : "aplica a"
    products           ||--o{ loyalty_promotion_products : "elegible"
    loyalty_promotions ||--o{ loyalty_redemptions : "canjeada"
    customers          ||--o{ loyalty_redemptions : "canjea"
    sales              ||--o| loyalty_redemptions : "registra canje"

    tenants {
        uuid id PK
        text name
        text status "active|suspended"
        text favicon_url "URL de /files/* (nullable)"
        timestamptz created_at
    }
    branches {
        uuid id PK
        uuid tenant_id FK
        text name "UNIQUE(tenant_id,name)"
        text status "active|inactive"
        timestamptz created_at
        timestamptz updated_at
    }
    users {
        uuid id PK
        uuid tenant_id FK "NULL solo super admin"
        citext email UK "único global"
        text password_hash "bcrypt"
        text name
        bool is_super_admin
        text status "active|disabled"
        timestamptz created_at
    }
    user_branches {
        uuid user_id PK,FK
        uuid branch_id PK,FK
        uuid tenant_id FK
        timestamptz created_at
    }
    categories {
        uuid id PK
        uuid tenant_id FK
        text name "UNIQUE(tenant_id,name)"
        text status "active|inactive"
        int sort_order
        text image_url
        timestamptz created_at
    }
    products {
        uuid id PK
        uuid tenant_id FK
        uuid category_id FK "opcional"
        text name "UNIQUE(tenant_id,name)"
        int price_cents "> 0"
        text status "active|inactive"
        text image_url
        timestamptz created_at
    }
    customers {
        uuid id PK
        uuid tenant_id FK
        text phone "UNIQUE(tenant_id,phone)"
        text first_name
        text last_name
        int visits "ciclo actual (lealtad)"
        int visits_lifetime "acumulado de por vida (nunca reinicia)"
        timestamptz created_at
    }
    sales {
        uuid id PK
        uuid tenant_id FK
        uuid branch_id FK "sucursal de la venta (nullable, derivada del usuario)"
        uuid customer_id FK "opcional"
        int total_cents "tras descuento"
        int discount_cents "descuento lealtad (servidor)"
        int amount_paid_cents
        int change_cents
        text payment_method "cash|card"
        timestamptz created_at
    }
    sale_items {
        uuid id PK
        uuid sale_id FK
        uuid product_id FK "snapshot"
        text name
        int unit_price_cents
        int quantity "> 0"
        int line_total_cents
    }
    loyalty_promotions {
        uuid id PK
        uuid tenant_id FK
        text name
        int discount_percent "1..100 (100 = gratis)"
        int visit_threshold "umbral de visitas"
        bool resets_counter "al aplicar: visits=0 + snapshot"
        text status "active|inactive"
        timestamptz created_at
        timestamptz updated_at
    }
    loyalty_promotion_products {
        uuid promotion_id PK,FK
        uuid product_id PK,FK
        uuid tenant_id FK
    }
    loyalty_redemptions {
        uuid id PK
        uuid tenant_id FK
        uuid customer_id FK
        uuid sale_id FK
        uuid promotion_id FK "ON DELETE SET NULL"
        text promotion_name "snapshot"
        int discount_percent "snapshot"
        int visit_threshold "snapshot"
        bool caused_reset
        int visits_cycle_at "ciclo alcanzado (post-incremento)"
        int visits_lifetime_at "acumulado en ese instante"
        int discount_cents
        timestamptz created_at
    }
```

## Reglas de negocio clave
- **Sucursales (ADR-006/ADR-007):** `branches` es una capa organizativa bajo el `tenants`
  único (`UNIQUE(tenant_id, name)`). El personal pertenece a **1+ sucursales** vía
  `user_branches` (M:N); el super admin las administra (solo super admin). Tras login, el
  usuario elige su **sucursal activa** (claim `activeBranchId` en el JWT, `POST
  /auth/select-branch`; auto si tiene 1). Esa sucursal etiqueta `sales.branch_id` **derivado
  en servidor** (NULL = "Sin sucursal") y el encabezado del POS (`Faro. {sucursal activa}`).
  `tenants.favicon_url` es el favicon por negocio (nullable → default). Coherencia
  branch↔tenant validada en servicio.
- **Lealtad por promociones** (visitas compartidas entre sucursales, ver ADR-005):
  `customers.visits` es el **contador de ciclo** y `customers.visits_lifetime` el acumulado
  de por vida. Cada venta pagada con cliente hace `visits += 1` y `visits_lifetime += 1`.
- Una **promoción** es aplicable cuando `visits ≥ visit_threshold`. El cajero aplica
  **manualmente una** promoción por venta; el servidor calcula `discount_cents` como
  `% × líneas de productos elegibles`, acotado al subtotal.
- Si la promoción tiene `resets_counter`, al aplicarla se escribe una fila en
  `loyalty_redemptions` (snapshot) y `visits → 0`; `visits_lifetime` no cambia.
- **Dinero** siempre en centavos (enteros).
- **Snapshot**: `sale_items` guarda `name`/`unit_price_cents`; `loyalty_redemptions` guarda
  el snapshot de la promoción al canjear.
