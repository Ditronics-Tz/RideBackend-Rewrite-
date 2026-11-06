// Command transport-acceptance serves only disposable local acceptance fixtures.
// It never reads application .env or connects to a database without the required
// test-name prefix. No test endpoints are registered in the production server.
package main

import (
	"context"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"log"
	"os"
	"ride-backend/jobs"
	"ride-backend/migrations"
	"ride-backend/models"
	"ride-backend/routes"
	"ride-backend/store"
	"ride-backend/testutil"
	"strings"
	"time"
)

func main() {
	c, err := pgx.ParseConfig(os.Getenv("RIDA_ACCEPTANCE_DSN"))
	if err != nil || !strings.HasPrefix(c.Database, "rida_acceptance_") {
		log.Fatal("explicit disposable rida_acceptance_ database required")
	}
	sql := stdlib.OpenDB(*c)
	defer sql.Close()
	if err = migrations.Up(context.Background(), sql); err != nil {
		log.Fatal(err)
	}
	db, err := gorm.Open(postgres.New(postgres.Config{Conn: sql}), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		log.Fatal("database unavailable")
	}
	s := store.New(db)
	_, cfg, err := testutil.SetupTestStore()
	if err != nil {
		log.Fatal(err)
	}
	cfg.RideBaseFareTZS = 1000
	cfg.RidePerKmFareTZS = 500
	cfg.RidePerMinuteFareTZS = 100
	cfg.RideEstimatedSpeedKMPH = 30
	cfg.RideSearchRadiusKM = 10
	cfg.RideOfferTimeoutSeconds = 120
	cfg.PublicBaseURL = "http://127.0.0.1:3005"
	s.ConfigureOperations(cfg)
	hash, _ := bcrypt.GenerateFromPassword([]byte("RidaAcceptance123!"), bcrypt.MinCost)
	var driver *models.User
	for _, role := range []models.Role{models.RoleDriver, models.RolePassenger} {
		email := string(role) + "@acceptance.test"
		u, exists := s.GetUserByEmail(email)
		if !exists {
			u, err = s.CreateUser("Acceptance "+string(role), email, string(hash), role)
			if err != nil {
				log.Fatal(err)
			}
		}
		if role == models.RoleDriver {
			driver = u
		}
	}
	var other models.User
	if err = db.Where("email=?", "other@acceptance.test").First(&other).Error; err != nil {
		_, err = s.CreateUser("Other passenger", "other@acceptance.test", string(hash), models.RolePassenger)
		if err != nil {
			log.Fatal(err)
		}
	}
	expires := time.Now().AddDate(1, 0, 0)
	for _, kind := range models.RequiredDocumentTypes {
		var count int64
		db.Model(&models.DriverDocument{}).Where("driver_id=? AND type=?", driver.ID, kind).Count(&count)
		if count == 0 {
			var expiry *time.Time
			if models.RequiresExpiry(kind) {
				expiry = &expires
			}
			if err = db.Create(&models.DriverDocument{ID: uuid.NewString(), DriverID: driver.ID, Type: kind, FilePath: "private/acceptance", Status: "approved", IsCurrent: true, ExpiresAt: expiry, CreatedAt: time.Now()}).Error; err != nil {
				log.Fatal(err)
			}
		}
	}
	if err = db.Model(&models.DriverProfile{}).Where("user_id=?", driver.ID).Updates(map[string]any{"licence_number": "ACCEPTANCE-LIC", "plate_number": "ACCEPTANCE-PLATE", "vehicle_card_number": "ACCEPTANCE-CARD", "passenger_seats": 3, "vehicle_type": "car", "verification_status": models.Approved, "is_online": true}).Error; err != nil {
		log.Fatal(err)
	}
	cfgFee, err := s.TransportSettings()
	if err != nil {
		log.Fatal(err)
	}
	if cfgFee.Version == 0 {
		_, err = s.SaveTransportSettings(models.TransportSettings{Enabled: true, SharedFee: 700, HailPercent: 12})
		if err != nil {
			log.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go jobs.RunRideDispatch(ctx, s, cfg)
	log.Print("Disposable acceptance API listening on 127.0.0.1:3005")
	log.Fatal(routes.BuildApp(cfg, s).Listen("127.0.0.1:3005"))
}
