package routes

import (
	"ride-backend/controllers"
	"ride-backend/middleware"

	"github.com/gofiber/fiber/v2"
)

func SetupRatingRoutes(app *fiber.App, rc *controllers.RatingController, jwtSecret string) {
	protected := middleware.Protected(jwtSecret)

	r := app.Group("/api/v1/ratings", protected)
	r.Post("/", rc.CreateRating)
	r.Get("/user/:userId", rc.ListRatingsForUser)
}
