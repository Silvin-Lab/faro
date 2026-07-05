# Plan de despliegue a producción — Faro (primera versión)

Estado: **borrador para revisión del humano**. Objetivo: llevar a producción la primera versión
(un solo negocio con sucursales, lealtad, POS) con costo/operación mínimos y camino claro a crecer.

> Convención de responsabilidad: lo marcado **[ING]** requiere trabajo de ingeniería (delegar al
> backend/frontend/devops cuando el equipo esté disponible); lo marcado **[OPS]** es configuración
> de infraestructura/despliegue; **[DEC]** es una decisión que necesita el humano.

---

## 1. Arquitectura destino (MVP)

```
[ Navegador / iPad ]
        │  HTTPS
        ▼
[ Frontend Next.js ]  ──llamadas API (cookie httpOnly)──►  [ Backend Go (Fly.io) ]
  (Vercel o Fly)                                                     │
                                                                     ▼
                                                            [ Postgres (Neon) ]
        imágenes /files/* ◄────────────────────────  [ Object storage (R2/S3) ]  [ING]
```

- **Backend**: contenedor Go (ya hay `Dockerfile` multi-stage → distroless) en **Fly.io**.
- **DB**: **Neon** (Postgres serverless, con SSL).
- **Frontend**: Next.js 14 (App Router). **[DEC]** host: **Vercel** (recomendado) o Fly.
- **Uploads** (favicon + imágenes de producto/categoría): hoy en disco local `UPLOAD_DIR`,
  servidos en `/files/*`. En Fly el FS es **efímero** → **[ING]** mover a object storage.

## 2. Decisiones abiertas (necesito tu confirmación) [DEC]
1. **Dominio**: ¿cuál? Recomiendo **mismo dominio raíz** para front y API (ej. `app.tudominio.com`
   + `api.tudominio.com`). Esto mantiene la cookie de sesión como **SameSite=Lax** (same-site entre
   subdominios) y **evita un cambio de código**. Si front y API quedan en dominios distintos
   (`*.vercel.app` vs `*.fly.dev`), la cookie tendría que ser **SameSite=None; Secure** → **[ING]**.
2. **Host del frontend**: Vercel (más simple para Next.js) vs Fly (todo en un proveedor).
3. **Object storage** para uploads: **Cloudflare R2** (sin egress, barato) vs AWS S3.
4. **Región**: sugiero una cercana a México (Fly `mia`/`dfw`; Neon `us-east`).

## 3. Trabajo de ingeniería previo (bloqueante para prod) [ING]
Estos son cambios de código que hay que delegar antes del go-live:
1. **Uploads → object storage**: reemplazar el guardado en disco por subida a R2/S3 (SDK) y servir
   `/files/*` desde el bucket/CDN (o URL firmada). Persistencia real e independiente del contenedor.
2. **Runner de migraciones**: hoy se aplican `0001–0012` a mano con `psql`. Añadir un mecanismo
   idempotente (tabla `schema_migrations` + comando `migrate`), y usarlo como **`release_command`**
   de Fly (corre antes de cada deploy). Sin esto, cada deploy exige migrar manualmente.
3. **Cookie cross-site (solo si front y API quedan en dominios distintos)**: `SameSite=None; Secure`.
   Evitable si se usa el mismo dominio raíz (ver Decisión #1).
4. **Config**: confirmar que `COOKIE_SECURE=true` y `CORS_ORIGIN` = dominio real del frontend en prod;
   revisar que el frontend lea `NEXT_PUBLIC_API_URL` en build/runtime.
5. (Opcional MVP) **Healthcheck**: ya existen `/health` y `/ready` (Fly usará `/ready`).

## 4. Secretos / variables por entorno (prod)
| Variable | Dónde | Valor prod |
|---|---|---|
| `DATABASE_URL` | Fly secret | connection string de Neon (con `sslmode=require`) |
| `JWT_SECRET` | Fly secret | **fuerte y aleatorio** (32+ bytes) |
| `COOKIE_SECURE` | Fly secret/env | `true` |
| `CORS_ORIGIN` | Fly secret/env | URL del frontend (ej. `https://app.tudominio.com`) |
| `FARO_SUPERADMIN_EMAIL` / `_PASSWORD` | Fly secret | credenciales del super admin (seed) |
| `UPLOAD_DIR` | env | irrelevante tras migrar a object storage; hasta entonces, Fly volume |
| `PORT` | env | `8080` |
| `NEXT_PUBLIC_API_URL` | Vercel/Fly env (frontend) | `https://api.tudominio.com` |

## 5. Pasos de despliegue (orden)
1. **[OPS]** Crear proyecto/DB en **Neon**; obtener `DATABASE_URL`. Habilitar backups automáticos.
2. **[OPS/ING]** Aplicar migraciones `0001–0012` a Neon (con el runner del punto 3.2, o una vez a
   mano con `psql` para el primer deploy).
3. **[ING]** Implementar uploads → object storage y crear el bucket (R2/S3) + credenciales.
4. **[OPS]** Backend en **Fly.io**: `fly launch` (genera `fly.toml`), configurar healthcheck a `/ready`,
   `fly secrets set` (tabla §4), `release_command = migrate`, `fly deploy`.
5. **[OPS]** Frontend en **Vercel** (o Fly): variable `NEXT_PUBLIC_API_URL`, build y deploy.
6. **[OPS]** DNS + TLS: apuntar `app.` y `api.` al frontend y backend; certificados (Fly/Vercel los
   emiten). Verificar cookie de sesión viaja (login desde el dominio real).
7. **[OPS]** **Seed inicial**: sembrar super admin (`FARO_SUPERADMIN_*` en el arranque). Crear el
   **negocio único (tenant)** — nota: `POST /tenants` se retiró (ADR-007), así que el tenant se
   siembra por script/bootstrap **[ING/OPS]** (definir el método: comando de seed o SQL controlado).
8. **[OPS]** Smoke test end-to-end en prod: login super admin → crear sucursal → crear usuario y
   asignar sucursal → login usuario de sucursal → venta → reporte.

## 6. CI/CD (GitHub Actions) [OPS]
- **CI** en cada PR: `go build/vet/test -p 1` (con una Postgres de servicio) + `tsc --noEmit`/lint del front.
- **CD**: al hacer merge a `main`, deploy automático (backend → Fly, frontend → Vercel). Los secretos
  viven en el proveedor, no en el repo.
- **Merge a `main`**: hoy el trabajo vive en `feat/loyalty-promotions-v2`; antes de prod, abrir PR(s)
  y mergear a `main` (deploy sale de `main`).

## 7. Seguridad (checklist)
- `JWT_SECRET` fuerte (no el default de dev). `COOKIE_SECURE=true`, `HttpOnly` (ya), `SameSite` correcto.
- `CORS_ORIGIN` exacto (un origen), `AllowCredentials=true` (ya).
- Contraseñas bcrypt (ya). Rotación del password del super admin tras el primer login.
- Neon con SSL obligatorio. Sin secretos en el repo (verificado: `.env`/`uploads/` gitignored).

## 8. Respaldos, rollback y observabilidad
- **Backups**: Neon tiene branching/PITR; habilitar y verificar restore.
- **Rollback**: Fly conserva versiones (`fly releases` / `fly deploy --image <anterior>`); Vercel tiene
  rollback de deploys. Migraciones: preferir cambios aditivos; documentar `down`.
- **Observabilidad**: logs de Fly/Vercel; healthchecks `/health` `/ready`. (Post-MVP: métricas/alertas.)

## 9. Checklist go-live
- [ ] Decisiones §2 confirmadas (dominio, host front, storage, región).
- [ ] [ING] uploads → object storage; runner de migraciones; cookie cross-site si aplica.
- [ ] Neon creada + migraciones aplicadas + backups on.
- [ ] Fly backend desplegado con secretos y `release_command`.
- [ ] Frontend desplegado con `NEXT_PUBLIC_API_URL`.
- [ ] DNS/TLS ok; login funciona desde el dominio real (cookie viaja).
- [ ] Super admin + negocio único sembrados; password del super admin rotado.
- [ ] Smoke test E2E en prod ok.
- [ ] CI/CD verde en `main`.

## 10. Riesgos top 3
1. **Uploads en FS efímero** → favicon/imágenes se pierden al redeploy. **Mitiga**: object storage (bloqueante).
2. **Cookie cross-site** si front/API en dominios distintos → sesión no viaja. **Mitiga**: mismo dominio raíz o `SameSite=None`.
3. **Migraciones manuales** → deploys frágiles/olvidos. **Mitiga**: runner + `release_command`.

---

### Resumen ejecutivo
Camino MVP: **Neon (DB) + Fly (backend) + Vercel (frontend)**, mismo dominio raíz para no tocar la
cookie. **Tres tareas de ingeniería** son prerrequisito (uploads→object storage, runner de
migraciones, y config de cookie/CORS/secure), y hay **4 decisiones** que necesito de ti (dominio,
host del frontend, proveedor de storage, región). Con eso, el resto es configuración de infra + un
smoke test E2E.
