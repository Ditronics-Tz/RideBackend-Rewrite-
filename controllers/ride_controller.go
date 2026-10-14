package controllers

import (
	"encoding/json"
	"math"
	"time"

	"github.com/gofiber/fiber/v2"
	"ride-backend/config"
	"ride-backend/middleware"
	"ride-backend/models"
	"ride-backend/store"
)

type RideController struct {
	Cfg   *config.Config
	Store *store.Store
}

func NewRideController(cfg *config.Config, s *store.Store) *RideController {
	return &RideController{Cfg: cfg, Store: s}
}

func (rc *RideController) fare(distanceMeters float64, duration time.Duration) (int64, error) {
	return store.RideFare(rc.Cfg, distanceMeters, duration)
}

func (rc *RideController) estimatedFare(distanceMeters float64) (int64, error) {
	return store.EstimatedRideFare(rc.Cfg, distanceMeters)
}

func (rc *RideController) RideResponse(c *fiber.Ctx, ride *models.Ride) (*models.RideResponse, error) {
	passenger, _ := rc.Store.GetUserByID(ride.PassengerID)
	if passenger == nil {
		return nil, store.OpError(503, "Ride participant is unavailable")
	}
	response := &models.RideResponse{
		ID: ride.ID, Status: ride.Status, Passenger: models.RideParty{ID: passenger.ID, Name: passenger.Name},
		Pickup:      models.RidePoint{Lat: ride.PickupLat, Lng: ride.PickupLng, Address: ride.Pickup},
		Dropoff:     models.RidePoint{Lat: ride.DropoffLat, Lng: ride.DropoffLng, Address: ride.Dropoff},
		VehicleType: ride.VehicleType, EstimatedFare: ride.EstimatedFare, CancelReason: ride.CancelReason, CreatedAt: ride.CreatedAt,
		AcceptedAt: ride.AcceptedAt, StartedAt: ride.StartedAt, CompletedAt: ride.CompletedAt,
		CancelledAt: ride.CancelledAt, Events: []models.RideEventResponse{},
	}
	if ride.Status == "completed" {
		fare := ride.Fare
		response.FinalFare = &fare
	}
	if ride.DriverID != models.UnassignedRideDriverID {
		driver, _ := rc.Store.GetUserByID(ride.DriverID)
		if driver != nil {
			response.Driver = &models.RideParty{ID: driver.ID, Name: driver.Name}
		}
	}
	events, err := rc.Store.RideEvents(c.UserContext(), ride.ID)
	if err != nil {
		return nil, err
	}
	for _, event := range events {
		row := models.RideEventResponse{Type: event.Type, ActorID: event.ActorID, ActorType: event.ActorType, At: event.At}
		if event.Meta != "" {
			_ = json.Unmarshal([]byte(event.Meta), &row.Meta)
		}
		response.Events = append(response.Events, row)
	}
	return response, nil
}

func (rc *RideController) Create(c *fiber.Ctx) error {
	passengerID, role := middleware.CurrentUser(c)
	if role != models.RolePassenger {
		return fiber.NewError(403, "Passenger account required")
	}
	var input models.CreateRideInput
	if len(c.Body()) > 4096 || c.BodyParser(&input) != nil {
		return fiber.NewError(400, "Invalid ride request")
	}
	var distance float64
	var err error
	input, distance, err = store.ValidateRideInput(input)
	if err != nil {
		return operationError(c, err)
	}
	fare, err := rc.estimatedFare(distance)
	if err != nil {
		return operationError(c, err)
	}
	ride, err := rc.Store.CreateRideRequest(c.UserContext(), passengerID, c.Get("Idempotency-Key"), input, distance, fare, rc.Cfg.RideSearchRadiusKM, time.Duration(rc.Cfg.RideOfferTimeoutSeconds)*time.Second)
	if err != nil {
		return operationError(c, err)
	}
	response, err := rc.RideResponse(c, ride)
	if err != nil {
		return operationError(c, err)
	}
	return c.Status(201).JSON(response)
}

func (rc *RideController) Quote(c *fiber.Ctx) error {
	var input models.CreateRideInput
	if len(c.Body()) > 4096 || c.BodyParser(&input) != nil {
		return fiber.NewError(400, "Invalid ride quote request")
	}
	_, distance, err := store.ValidateRideInput(input)
	if err != nil {
		return operationError(c, err)
	}
	duration, err := store.EstimatedRideDuration(rc.Cfg, distance)
	if err != nil {
		return operationError(c, err)
	}
	fare, err := rc.fare(distance, duration)
	if err != nil {
		return operationError(c, err)
	}
	c.Set("Cache-Control", "no-store")
	return c.JSON(models.RideQuote{EstimatedFareTZS: fare, EstimatedDistanceMeters: int64(math.Ceil(distance)), EstimatedDurationSeconds: int64(duration.Seconds()), DistanceBasis: "straight_line", Currency: "TZS", PaymentMethod: "cash"})
}

func (rc *RideController) DriverLocation(c *fiber.Ctx) error {
	uid, _ := middleware.CurrentUser(c)
	location, err := rc.Store.CurrentRideDriverLocation(c.UserContext(), c.Params("id"), uid)
	if err != nil {
		return operationError(c, err)
	}
	c.Set("Cache-Control", "no-store")
	if location == nil {
		return c.SendStatus(204)
	}
	return c.JSON(location)
}

func (rc *RideController) Cancel(c *fiber.Ctx) error {
	uid, _ := middleware.CurrentUser(c)
	var input struct {
		Reason string `json:"reason"`
	}
	if len(c.Body()) > 2048 || c.BodyParser(&input) != nil {
		return fiber.NewError(400, "Invalid cancellation request")
	}
	ride, err := rc.Store.CancelRide(c.UserContext(), uid, c.Params("id"), c.Get("Idempotency-Key"), input.Reason)
	if err != nil {
		return operationError(c, err)
	}
	response, err := rc.RideResponse(c, ride)
	if err != nil {
		return operationError(c, err)
	}
	return c.JSON(response)
}

func (rc *RideController) Offers(c *fiber.Ctx) error {
	uid, _ := middleware.CurrentUser(c)
	rides, err := rc.Store.DriverRideOffers(c.UserContext(), uid, rc.Cfg.RideSearchRadiusKM, time.Duration(rc.Cfg.RideOfferTimeoutSeconds)*time.Second)
	if err != nil {
		return operationError(c, err)
	}
	result := make([]models.RideResponse, 0, len(rides))
	for i := range rides {
		response, err := rc.RideResponse(c, &rides[i])
		if err != nil {
			return operationError(c, err)
		}
		result = append(result, *response)
	}
	return c.JSON(fiber.Map{"offers": result})
}

func (rc *RideController) Accept(c *fiber.Ctx) error {
	uid, _ := middleware.CurrentUser(c)
	ride, err := rc.Store.AcceptRide(c.UserContext(), uid, c.Params("id"), c.Get("Idempotency-Key"))
	if err != nil {
		return operationError(c, err)
	}
	response, err := rc.RideResponse(c, ride)
	if err != nil {
		return operationError(c, err)
	}
	return c.JSON(response)
}

func (rc *RideController) Decline(c *fiber.Ctx) error {
	uid, _ := middleware.CurrentUser(c)
	ride, err := rc.Store.DeclineRide(c.UserContext(), uid, c.Params("id"), c.Get("Idempotency-Key"), rc.Cfg.RideSearchRadiusKM, time.Duration(rc.Cfg.RideOfferTimeoutSeconds)*time.Second)
	if err != nil {
		return operationError(c, err)
	}
	response, err := rc.RideResponse(c, ride)
	if err != nil {
		return operationError(c, err)
	}
	return c.JSON(response)
}

func (rc *RideController) Arrived(c *fiber.Ctx) error  { return rc.transition(c, "arrived") }
func (rc *RideController) Start(c *fiber.Ctx) error    { return rc.transition(c, "start") }
func (rc *RideController) Complete(c *fiber.Ctx) error { return rc.transition(c, "complete") }

func (rc *RideController) Rate(c *fiber.Ctx) error {
	userID, _ := middleware.CurrentUser(c)
	var input models.RateRideInput
	if len(c.Body()) > 4096 || c.BodyParser(&input) != nil {
		return fiber.NewError(400, "Invalid rating")
	}
	rating, err := rc.Store.RateCompletedRide(c.UserContext(), userID, c.Params("id"), input)
	if err != nil {
		return operationError(c, err)
	}
	return c.Status(201).JSON(rating)
}

func (rc *RideController) transition(c *fiber.Ctx, action string) error {
	uid, _ := middleware.CurrentUser(c)
	var finalFare int64
	if action == "complete" {
		ride, err := rc.Store.GetRideForParticipant(c.UserContext(), c.Params("id"), uid)
		if err != nil || ride.DriverID != uid || ride.StartedAt == nil {
			return fiber.NewError(409, "Ride cannot be completed")
		}
		now := time.Now()
		if rc.Store.Clock != nil {
			now = rc.Store.Clock()
		}
		finalFare, err = rc.fare(ride.ExpectedDistance, now.Sub(*ride.StartedAt))
		if err != nil {
			return operationError(c, err)
		}
	}
	ride, err := rc.Store.TransitionRide(c.UserContext(), uid, c.Params("id"), c.Get("Idempotency-Key"), action, finalFare)
	if err != nil {
		return operationError(c, err)
	}
	response, err := rc.RideResponse(c, ride)
	if err != nil {
		return operationError(c, err)
	}
	return c.JSON(response)
}

func (rc *RideController) Detail(c *fiber.Ctx) error {
	uid, _ := middleware.CurrentUser(c)
	ride, err := rc.Store.GetRideForParticipant(c.UserContext(), c.Params("id"), uid)
	if err != nil {
		return operationError(c, err)
	}
	response, err := rc.RideResponse(c, ride)
	if err != nil {
		return operationError(c, err)
	}
	return c.JSON(response)
}

func (rc *RideController) List(c *fiber.Ctx) error {
	uid, _ := middleware.CurrentUser(c)
	page, err := operationPage(c)
	if err != nil {
		return operationError(c, err)
	}
	var from, to *time.Time
	if raw := c.Query("from"); raw != "" {
		v, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return operationError(c, store.FieldError("from", "from must be RFC3339"))
		}
		from = &v
	}
	if raw := c.Query("to"); raw != "" {
		v, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return operationError(c, store.FieldError("to", "to must be RFC3339"))
		}
		to = &v
	}
	if from != nil && to != nil && !to.After(*from) {
		return operationError(c, store.FieldError("to", "to must be later than from"))
	}
	rides, total, err := rc.Store.ListRidesForParticipant(c.UserContext(), uid, c.Query("status"), from, to, page)
	if err != nil {
		return operationError(c, err)
	}
	result := make([]models.RideResponse, 0, len(rides))
	for i := range rides {
		response, err := rc.RideResponse(c, &rides[i])
		if err != nil {
			return operationError(c, err)
		}
		result = append(result, *response)
	}
	return c.JSON(fiber.Map{"rides": result, "total": total, "limit": page.Limit, "offset": page.Offset})
}

func (rc *RideController) Location(c *fiber.Ctx) error {
	uid, _ := middleware.CurrentUser(c)
	var input struct {
		Lat      float64 `json:"lat"`
		Lng      float64 `json:"lng"`
		Accuracy float64 `json:"accuracy"`
	}
	if len(c.Body()) > 2048 || c.BodyParser(&input) != nil {
		return fiber.NewError(400, "Invalid location")
	}
	if err := rc.Store.SetDriverLocation(c.UserContext(), uid, c.Get("Idempotency-Key"), input.Lat, input.Lng, input.Accuracy); err != nil {
		return operationError(c, err)
	}
	return c.JSON(fiber.Map{"updated": true})
}

func (rc *RideController) SetAvailability(c *fiber.Ctx) error {
	uid, role := middleware.CurrentUser(c)
	if role != models.RoleDriver {
		return fiber.NewError(403, "Driver account required")
	}
	var input struct {
		Online *bool `json:"online"`
	}
	if len(c.Body()) > 2048 || c.BodyParser(&input) != nil || input.Online == nil {
		return fiber.NewError(400, "online boolean is required")
	}
	if err := rc.Store.SetDriverAvailability(c.UserContext(), uid, c.Get("Idempotency-Key"), *input.Online); err != nil {
		return operationError(c, err)
	}
	return c.JSON(fiber.Map{"online": *input.Online})
}
