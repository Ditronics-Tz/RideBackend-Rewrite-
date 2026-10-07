package routes

import (
	"ride-backend/controllers"

	"github.com/gofiber/fiber/v2"
)

func SetupFileRoutes(router fiber.Router, fc *controllers.FileController) {
	files := router.Group("/files")
	files.Get("/licences/:id", fc.GetLicence)
}
