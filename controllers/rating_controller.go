package controllers

import (
	"ride-backend/config"
	"ride-backend/middleware"
	"ride-backend/models"
	"ride-backend/store"

	"github.com/gofiber/fiber/v2"
)

type RatingController struct {
	Cfg   *config.Config
	Store *store.Store
}

func NewRatingController(cfg *config.Config, s *store.Store) *RatingController {
	return &RatingController{Cfg: cfg, Store: s}
}

// CreateRating — any authenticated user rates another user (1-5).
func (rc *RatingController) CreateRating(c *fiber.Ctx) error {
	fromID, _ := middleware.CurrentUser(c)
	var input models.RatingInput
	if err := c.BodyParser(&input); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid request body"})
	}
	if input.ToUserID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "to_user_id is required"})
	}
	r, err := rc.Store.CreateRating(fromID, input.ToUserID, input.Score, input.Comment)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.Status(fiber.StatusCreated).JSON(r)
}

// ListRatingsForUser — ratings + average for a user (driver or passenger).
func (rc *RatingController) ListRatingsForUser(c *fiber.Ctx) error {
	id := c.Params("userId")
	if _, ok := rc.Store.GetUserByID(id); !ok {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "User not found"})
	}
	ratings, avg, total := rc.Store.ListRatingsForUser(id)
	return c.JSON(fiber.Map{
		"user_id":        id,
		"average_rating": avg,
		"total_ratings":  total,
		"ratings":        ratings,
	})
}
