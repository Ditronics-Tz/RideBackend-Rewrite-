package controllers

import (
	"encoding/json"
	"math"
	"strings"
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
	cfg := rc.Cfg
	if cfg.RideBaseFareTZS <= 0 || cfg.RidePerKmFareTZS <= 0 || cfg.RidePerMinuteFareTZS <= 0 || cfg.RideEstimatedSpeedKMPH <= 0 {
		return 0, store.OpError(503, "Ride fares are not configured")
	}
	kilometers := math.Ceil(distanceMeters / 1000)
	minutes := math.Ceil(duration.Minutes())
	if minutes < 1 {
		minutes = 1
	}
	if kilometers < 0 || minutes < 0 || kilometers > float64(store.MaxMoney) || minutes > float64(store.MaxMoney) {
		return 0, store.FieldError("fare", "ride exceeds supported fare range")
	}
	kmFare, minuteFare := int64(kilometers), int64(minutes)
	if cfg.RidePerKmFareTZS > 0 && kmFare > (store.MaxMoney-cfg.RideBaseFareTZS)/cfg.RidePerKmFareTZS {
		return 0, store.FieldError("fare", "ride exceeds supported fare range")
	}
	total := cfg.RideBaseFareTZS + kmFare*cfg.RidePerKmFareTZS
	if cfg.RidePerMinuteFareTZS > 0 && minuteFare > (store.MaxMoney-total)/cfg.RidePerMinuteFareTZS {
		return 0, store.FieldError("fare", "ride exceeds supported fare range")
	}
	total += minuteFare * cfg.RidePerMinuteFareTZS
	if total <= 0 || total > store.MaxMoney {
		return 0, store.FieldError("fare", "ride exceeds supported fare range")
	}
	return total, nil
}

func (rc *RideController) estimatedFare(distanceMeters float64) (int64, error) {
	speed := rc.Cfg.RideEstimatedSpeedKMPH
	if speed <= 0 {
		return 0, store.OpError(503, "Ride fare estimation is not configured")
	}
	minutes := math.Ceil((distanceMeters / 1000) / float64(speed) * 60)
	return rc.fare(distanceMeters, time.Duration(minutes)*time.Minute)
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
	input.Pickup.Address = strings.TrimSpace(input.Pickup.Address)
	input.Dropoff.Address = strings.TrimSpace(input.Dropoff.Address)
	input.VehicleType = strings.TrimSpace(input.VehicleType)
	if input.Pickup.Address == "" || len([]rune(input.Pickup.Address)) > 250 || input.Dropoff.Address == "" || len([]rune(input.Dropoff.Address)) > 250 {
		return operationError(c, store.FieldError("address", "pickup and dropoff addresses must contain 1-250 characters"))
	}
	for name, point := range map[string]models.RidePoint{"pickup": input.Pickup, "dropoff": input.Dropoff} {
		if point.Lat < -90 || point.Lat > 90 || point.Lng < -180 || point.Lng > 180 {
			return operationError(c, store.FieldError(name, "latitude or longitude is outside the supported range"))
		}
	}
	if len([]rune(input.VehicleType)) > 50 {
		return operationError(c, store.FieldError("vehicle_type", "vehicle type must be at most 50 characters"))
	}
	distance := store.RideDistanceMeters(input.Pickup.Lat, input.Pickup.Lng, input.Dropoff.Lat, input.Dropoff.Lng)
	if distance < 1 {
		return operationError(c, store.FieldError("dropoff", "pickup and dropoff must be different locations"))
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
