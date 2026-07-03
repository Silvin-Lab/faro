# Modelo ER — Faro (hasta migración 0010)

Multi-tenant: todo dato de negocio cuelga de `tenants`. El super admin global es un
`users` con `tenant_id NULL`. Clientes y lealtad son a nivel negocio → se comparten
entre sucursales automáticamente.

> **v2 lealtad (migración 0010, ver ADR-005):** se pasa de config única a **promociones**.
> Se eliminan `loyalty_configs`, `loyalty_discount_products`, `loyalty_free_products`;
> entran `loyalty_promotions`, `loyalty_promotion_products`, `loyalty_redemptions`.
> `customers` gana `visits_lifetime`; `sales` pierde `loyalty_reward`.

```mermaid
erDiagram
    tenants ||--o{ users        : "emplea"
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
        timestamptz created_at
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
