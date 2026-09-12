// Package server arma el router HTTP y el middleware base del backend.
package server

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/jackc/pgx/v5/pgxpool"

	"faro/internal/auth"
	"faro/internal/bakery"
	"faro/internal/branches"
	"faro/internal/categories"
	"faro/internal/customers"
	"faro/internal/expenses"
	"faro/internal/insights"
	"faro/internal/loyalty"
	"faro/internal/products"
	"faro/internal/reports"
	"faro/internal/sales"
	"faro/internal/settings"
	"faro/internal/supplies"
	"faro/internal/uploads"
	"faro/internal/warehouse"
)

// New construye el handler HTTP raíz. Los módulos (auth, products, …) montarán
// aquí sus sub-routers en incrementos siguientes. corsOrigin es el origen del
// frontend (faro-ui) autorizado a consumir la API con credenciales.
func New(pool *pgxpool.Pool, corsOrigin string, authSvc *auth.Service, catSvc *categories.Service, prodSvc *products.Service, salesSvc *sales.Service, custSvc *customers.Service, reportsSvc *reports.Service, insightsSvc *insights.Service, loyaltySvc *loyalty.Service, branchesSvc *branches.Service, settingsSvc *settings.Service, expensesSvc *expenses.Service, suppliesSvc *supplies.Service, warehouseSvc *warehouse.Service, bakerySvc *bakery.Service, uploadsH *uploads.Handler, uploadDir string) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	// CORS para el frontend (faro-ui), que vive en otro origen.
	// AllowCredentials=true para que viaje la cookie de sesión httpOnly.
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{corsOrigin},
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Content-Type", "Authorization"},
		AllowCredentials: true,
		MaxAge:           300,
	}))

	// Liveness: el proceso está vivo.
	r.Get("/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	// Readiness: además la base de datos responde. NO lo pollea el health check de
	// Fly (eso mantenía a Neon despierto 24/7); queda para diagnóstico manual, por
	// eso el ping es en vivo y sin caché: en debug querés el estado real de la DB.
	r.Get("/ready", func(w http.ResponseWriter, req *http.Request) {
		if err := pool.Ping(req.Context()); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "db_unavailable"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	})

	// Módulo auth (login): /auth/login, /auth/logout, /auth/me, /auth/select-branch
	r.Mount("/auth", authSvc.Routes())
	// Usuarios (M7 v2): gestión de personal con membresías M:N (solo super admin).
	r.Mount("/users", authSvc.UserRoutes())
	// Módulo categorías (M2): lectura por sesión, escritura solo super admin.
	r.Mount("/categories", catSvc.Routes(authSvc.RequireSession, authSvc.RequireSuperAdmin))
	// Módulo productos (M3): lectura por sesión, escritura solo super admin.
	r.Mount("/products", prodSvc.Routes(authSvc.RequireSession, authSvc.RequireSuperAdmin))
	// Módulo POS (M4): ventas, total calculado en servidor.
	r.Mount("/sales", salesSvc.Routes(authSvc.RequireSession))
	// Clientes (lealtad): alta y búsqueda por teléfono.
	r.Mount("/customers", custSvc.Routes(authSvc.RequireSession))
	// Reportes (M5/M8): agregados de ventas por rango. Autorización por rol dentro
	// del handler: super admin (todas), branch_admin (su sucursal), resto 403.
	r.Mount("/reports", reportsSvc.Routes(authSvc.RequireSession))
	// Insights (M9): métricas de comportamiento (solo lectura, sin IA). Autorización
	// por rol dentro del handler: super admin (todas), branch_admin (su sucursal),
	// cashier/barista 403.
	r.Mount("/insights", insightsSvc.Routes(authSvc.RequireSession))
	// Lealtad (M6 v2): lectura por sesión, CRUD de promociones solo super admin.
	r.Mount("/loyalty", loyaltySvc.Routes(authSvc.RequireSession, authSvc.RequireSuperAdmin))
	// Sucursales (M7): CRUD solo super admin; GET /branches además lo lee el
	// repostero (M10) con autorización inline; ver branches.Routes.
	r.Mount("/branches", branchesSvc.Routes(authSvc.RequireSession, authSvc.RequireSuperAdmin))
	// Ajustes del negocio (M7): favicon del tenant (solo super admin).
	r.Mount("/settings", settingsSvc.Routes(authSvc.RequireSuperAdmin))
	// Gastos: catálogo (lectura sesión, escritura super admin) y registro de gastos
	// por sucursal (cualquier rol de sucursal, authz fina en el handler).
	r.Mount("/expenses", expensesSvc.Routes(authSvc.RequireSession, authSvc.RequireSuperAdmin))
	// Insumos: catálogo/existencias/recetas. Lectura por sesión; escritura (catálogo,
	// movimientos de inventario y recetas) solo super admin. Inventario por sucursal.
	r.Mount("/supplies", suppliesSvc.Routes(authSvc.RequireSession, authSvc.RequireSuperAdmin))
	// Almacén central (M8): existencias/mín-máx, proveedores, compras, salidas y
	// mermas. Gated a super admin salvo GET /stock, abierto también a repostero (M10
	// F17) con autorización inline; ver warehouse.Routes.
	r.Mount("/warehouse", warehouseSvc.Routes(authSvc.RequireSession, authSvc.RequireSuperAdmin))
	// Repostería / producción central (M10): pedidos de sucursal, producción (doble
	// efecto de stock), stock de postre y auditoría. Autorización inline por rol.
	r.Mount("/bakery", bakerySvc.Routes(authSvc.RequireSession))
	// Subida de imágenes (POST, solo super admin) y servir archivos estáticos (público).
	r.Mount("/uploads", uploadsH.Routes())
	r.Handle("/files/*", http.StripPrefix("/files/", http.FileServer(http.Dir(uploadDir))))

	return r
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
