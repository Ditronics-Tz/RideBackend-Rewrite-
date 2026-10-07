package routes

import (
	"ride-backend/config"
	"ride-backend/controllers"
	"ride-backend/middleware"

	"github.com/gofiber/fiber/v2"
)

func SetupRatingRoutes(router fiber.Router, rc *controllers.RatingController, cfg *config.Config) {
	protected := middleware.AppProtected(cfg)

	r := router.Group("/ratings", protected)
	r.Post("/", rc.CreateRating)
	r.Get("/user/:userId", rc.ListRatingsForUser)
}
