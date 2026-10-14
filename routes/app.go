package routes

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"time"

	"ride-backend/config"
	"ride-backend/controllers"
	"ride-backend/middleware"
	"ride-backend/store"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/helmet"
	"github.com/gofiber/fiber/v2/middleware/recover"
)

// BuildApp initializes the Fiber app with all middlewares and mounts all routes under /api/v1.
func BuildApp(cfg *config.Config, appStore *store.Store) *fiber.App {
	appStore.ConfigureOperations(cfg)
	// Tests may inject an offline storage adapter. Startup configures real
	// storage before BuildApp; local development retains legacy upload routes.
	if appStore.Storage == nil && cfg.StorageDriver != "r2" {
		if err := appStore.ConfigureStorage(cfg); err != nil {
			panic("Unable to configure local object storage")
		}
	}
	app := fiber.New(fiber.Config{
		AppName:      "Ride Backend Service",
		BodyLimit:    10 * 1024 * 1024,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
		ErrorHandler: func(c *fiber.Ctx, err error) error {
			code := fiber.StatusInternalServerError
			msg := "Internal Server Error"
			var e *fiber.Error
			if errors.As(err, &e) {
				code = e.Code
				msg = e.Message
			}
			return c.Status(code).JSON(fiber.Map{
				"error":      msg,
				"request_id": middleware.RequestID(c),
			})
		},
	})
	app.Use(middleware.RequestContext(slog.New(slog.NewJSONHandler(os.Stdout, nil))))
	app.Use(recover.New())
	started := time.Now()
	health := func(c *fiber.Ctx) error { return c.JSON(fiber.Map{"status": "ok", "version": "1.0.0"}) }
	ready := func(c *fiber.Ctx) error {
		ctx, cancel := context.WithTimeout(c.UserContext(), 2*time.Second)
		defer cancel()
		sqlDB, err := appStore.DB().DB()
		if err == nil {
			err = sqlDB.PingContext(ctx)
		}
		if err != nil {
			return c.Status(503).JSON(fiber.Map{"error": "database unavailable", "status": "unavailable", "database": "unavailable", "version": "1.0.0", "uptime_seconds": int64(time.Since(started).Seconds())})
		}
		return c.JSON(fiber.Map{"status": "ok", "database": "ok", "version": "1.0.0", "uptime_seconds": int64(time.Since(started).Seconds())})
	}

	// Root group: /api/v1
	v1 := app.Group("/api/v1")

	// Global security and logging middlewares on /api/v1
	v1.Use(helmet.New())
	// Allow cross-origin clients and answer preflight before authentication.
	v1.Use(cors.New(cors.Config{
		AllowOriginsFunc: func(origin string) bool { return config.OriginAllowed(cfg.CORSAllowedOrigins, origin) },
		AllowMethods:     "GET,POST,PUT,PATCH,DELETE,HEAD,OPTIONS",
		AllowHeaders:     "Origin,Content-Type,Accept,Authorization,Idempotency-Key,X-Request-ID,X-Device-ID,X-Device-Platform,X-Platform",
		ExposeHeaders:    "X-Request-ID,Retry-After",
	}))

	// Health endpoint (no auth)
	v1.Get("/health", health)
	v1.Get("/ready", ready)
	app.Get("/health", health)
	app.Get("/ready", ready)

	// Static public uploads (profile pictures)
	v1.Static("/uploads", "./uploads/public")

	// Controllers
	authController := controllers.NewAuthController(cfg, appStore)
	profileController := controllers.NewProfileController(cfg, appStore)
	ratingController := controllers.NewRatingController(cfg, appStore)
	staffAuthController := controllers.NewStaffAuthController(cfg, appStore)
	adminController := controllers.NewAdminController(cfg, appStore)
	fileController := controllers.NewFileController(cfg, appStore)

	// Mount router groups on /api/v1
	SetupAuthRoutes(v1, authController, cfg)
	SetupProfileRoutes(v1, profileController, cfg)
	SetupRideRoutes(v1, controllers.NewRideController(cfg, appStore), cfg, appStore)
	SetupPassengerRoutes(v1, cfg, appStore)
	SetupRatingRoutes(v1, ratingController, cfg)
	SetupStaffAuthRoutes(v1, staffAuthController, cfg, appStore)
	SetupAdminRoutes(v1, adminController, cfg, appStore)
	SetupFileRoutes(v1, fileController)
	SetupPromoAppRoutes(v1, controllers.NewPromoController(cfg, appStore), cfg, appStore)
	SetupDataRequestRoutes(v1, controllers.NewDataRequestController(cfg, appStore), cfg, appStore)
	SetupFinanceAppRoutes(v1, controllers.NewOperationsController(cfg, appStore), cfg, appStore)
	SetupOperationsAppRoutes(v1, controllers.NewOperationsController(cfg, appStore), cfg, appStore)

	return app
}
