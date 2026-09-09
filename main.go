package main

import (
	"log"
	"ride-backend/config"
	"ride-backend/controllers"
	"ride-backend/routes"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/logger"
)

package main

import (
"log"

"ride-backend/config"
"ride-backend/controllers"
"ride-backend/routes"

"github.com/gofiber/fiber/v2"
"github.com/gofiber/fiber/v2/middleware/logger"
)

func main() {
	cfg := config.LoadConfig()

	app := fiber.New(fiber.Config{
		AppName: "Ride Backend Service",
	})

	// Global Middlewares
	app.Use(logger.New())

	// Initialize Controllers
	authController := controllers.NewAuthController(cfg)

	// Setup Routes
	routes.SetupAuthRoutes(app, authController)

	log.Printf("Server starting on port %s...", cfg.Port)
	log.Fatal(app.Listen(":" + cfg.Port))
}