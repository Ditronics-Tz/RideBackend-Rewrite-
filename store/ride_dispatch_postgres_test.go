package store

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"ride-backend/migrations"
	"ride-backend/models"
)

func ridePostgresStore(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if os.Getenv("RUN_POSTGRES_RIDE_TESTS") != "1" || dsn == "" {
		t.Skip("Set RUN_POSTGRES_RIDE_TESTS=1 and POSTGRES_TEST_DSN for isolated-schema ride concurrency tests")
	}
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal("invalid PostgreSQL test configuration")
	}
	admin := stdlib.OpenDB(*config)
	schema := "ride_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := admin.Exec("CREATE SCHEMA " + schema); err != nil {
		admin.Close()
		t.Fatal("isolated ride schema unavailable")
	}
	config.RuntimeParams["search_path"] = schema
	connection := stdlib.OpenDB(*config)
	t.Cleanup(func() { connection.Close(); _, _ = admin.Exec("DROP SCHEMA " + schema + " CASCADE"); admin.Close() })
	if err := migrations.Up(context.Background(), connection); err != nil {
		t.Fatal("isolated ride migrations failed")
	}
	database, err := gorm.Open(postgres.New(postgres.Config{Conn: connection}), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal("test connection unavailable")
	}
	return New(database)
}

func TestPostgresConcurrentDispatchAssignsDriverToOnlyOneRide(t *testing.T) {
	s := ridePostgresStore(t)
	passengers := make([]*models.User, 2)
	for i := range passengers {
		user, err := s.CreateUser("Passenger", uuid.NewString()+"@example.test", "unused", models.RolePassenger)
		if err != nil {
			t.Fatal("passenger fixture failed")
		}
		passengers[i] = user
	}
	driver, err := s.CreateUser("Driver", uuid.NewString()+"@example.test", "unused", models.RoleDriver)
	if err != nil {
		t.Fatal("driver fixture failed")
	}
	if _, err := s.UpdateDriverIdentifiers(driver.ID, models.DriverProfileInput{LicenceNumber: "LIC" + driver.ID, PlateNumber: "PL" + driver.ID, VehicleCardNumber: "VC" + driver.ID}); err != nil {
		t.Fatal("driver identifiers fixture failed")
	}
	now := time.Now().UTC()
	expires := now.Add(365 * 24 * time.Hour)
	for _, kind := range models.RequiredDocumentTypes {
		var expiry *time.Time
		if models.RequiresExpiry(kind) {
			expiry = &expires
		}
		if err := s.db.Create(&models.DriverDocument{ID: uuid.NewString(), DriverID: driver.ID, Type: kind, FilePath: "private/test", Status: "approved", IsCurrent: true, ExpiresAt: expiry, CreatedAt: now}).Error; err != nil {
			t.Fatal("driver document fixture failed")
		}
	}
	if err := s.db.Model(&models.DriverProfile{}).Where("user_id = ?", driver.ID).Updates(map[string]any{"is_online": true, "verification_status": models.Approved, "vehicle_type": "car"}).Error; err != nil {
		t.Fatal("driver availability fixture failed")
	}
	if err := s.SetDriverLocation(context.Background(), driver.ID, "location-key-0001", -6.8, 39.2, 10); err != nil {
		t.Fatal("driver location fixture failed")
	}
	input := models.CreateRideInput{Pickup: models.RidePoint{Lat: -6.8, Lng: 39.2, Address: "Pickup"}, Dropoff: models.RidePoint{Lat: -6.81, Lng: 39.2, Address: "Dropoff"}, VehicleType: "car"}
	start := make(chan struct{})
	results := make(chan *models.Ride, 2)
	errors := make(chan error, 2)
	var workers sync.WaitGroup
	for i := range passengers {
		workers.Add(1)
		go func(passengerID string, index int) {
			defer workers.Done()
			<-start
			ride, err := s.CreateRideRequest(context.Background(), passengerID, "dispatch-key-000"+string(rune('1'+index)), input, RideDistanceMeters(input.Pickup.Lat, input.Pickup.Lng, input.Dropoff.Lat, input.Dropoff.Lng), 1500, 10, 30*time.Second)
			if err != nil {
				errors <- err
				return
			}
			results <- ride
		}(passengers[i].ID, i)
	}
	close(start)
	workers.Wait()
	close(results)
	close(errors)
	for range errors {
		t.Fatal("concurrent ride request failed")
	}
	offered := 0
	for ride := range results {
		if ride.Status == "offered" && ride.DriverID == driver.ID {
			offered++
		}
	}
	if offered != 1 {
		t.Fatalf("driver was assigned to %d concurrent rides, want exactly one", offered)
	}
}
