package main

import (
	"log"
	"ride-backend/config"
	"ride-backend/controllers"
	"ride-backend/db"
	"ride-backend/routes"
	"ride-backend/store"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/logger"
)

func main() {
	cfg := config.LoadConfig()

	// Postgres connection + auto-migrations
	gdb := db.Connect(cfg)
	appStore := store.New(gdb)

	app := fiber.New(fiber.Config{
		AppName: "Ride Backend Service",
		// Allow image uploads (licence + profile pictures)
		BodyLimit: 10 * 1024 * 1024,
	})

	// Global Middlewares
	app.Use(logger.New())

	// Uploaded images (licence, profile pictures)
	app.Static("/uploads", "./uploads")

	// Initialize Controllers
	authController := controllers.NewAuthController(cfg, appStore)
	profileController := controllers.NewProfileController(cfg, appStore)
	ratingController := controllers.NewRatingController(cfg, appStore)
	adminController := controllers.NewAdminController(cfg, appStore)

	// Setup Routes
	routes.SetupAuthRoutes(app, authController)
	routes.SetupProfileRoutes(app, profileController, cfg.JWTSecret)
	routes.SetupRatingRoutes(app, ratingController, cfg.JWTSecret)
	routes.SetupAdminRoutes(app, adminController, cfg.JWTSecret)

	log.Printf("Server starting on port %s...", cfg.Port)
	log.Fatal(app.Listen(":" + cfg.Port))
}
