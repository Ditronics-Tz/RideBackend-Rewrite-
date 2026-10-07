package routes

import (
	"errors"

	"ride-backend/config"
	"ride-backend/controllers"
	"ride-backend/store"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/helmet"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/gofiber/fiber/v2/middleware/recover"
)

// BuildApp initializes the Fiber app with all middlewares and mounts all routes under /api/v1.
func BuildApp(cfg *config.Config, appStore *store.Store) *fiber.App {
	app := fiber.New(fiber.Config{
		AppName:   "Ride Backend Service",
		BodyLimit: 10 * 1024 * 1024,
		ErrorHandler: func(c *fiber.Ctx, err error) error {
			code := fiber.StatusInternalServerError
			msg := "Internal Server Error"
			var e *fiber.Error
			if errors.As(err, &e) {
				code = e.Code
				msg = e.Message
			}
			return c.Status(code).JSON(fiber.Map{
				"error": msg,
			})
		},
	})

	// Root group: /api/v1
	v1 := app.Group("/api/v1")

	// Global security and logging middlewares on /api/v1
	v1.Use(recover.New())
	v1.Use(helmet.New())
	v1.Use(logger.New())
	if cfg.CORSAllowedOrigins != "" {
		v1.Use(cors.New(cors.Config{
			AllowOrigins: cfg.CORSAllowedOrigins,
		}))
	}

	// Health endpoint (no auth)
	v1.Get("/health", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{
			"status":  "ok",
			"version": "1.0.0",
		})
	})

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
	SetupRatingRoutes(v1, ratingController, cfg)
	SetupStaffAuthRoutes(v1, staffAuthController, cfg, appStore)
	SetupAdminRoutes(v1, adminController, cfg, appStore)
	SetupFileRoutes(v1, fileController)

	return app
}
