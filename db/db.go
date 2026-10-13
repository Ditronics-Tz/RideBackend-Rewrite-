package db

import (
	"fmt"
	"log"

	"ride-backend/config"
	"ride-backend/models"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Open creates a connection only. Production schema changes run via cmd/migrate.
func Open(cfg *config.Config) (*gorm.DB, error) {
	gdb, err := gorm.Open(postgres.Open(cfg.DSN()), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Postgres")
	}

	return gdb, nil
}

// Connect opens the Postgres connection and logs fatally if connection fails.
func Connect(cfg *config.Config) *gorm.DB {
	gdb, err := Open(cfg)
	if err != nil {
		log.Fatalf("%v", err)
	}
	log.Println("Connected to Postgres")
	return gdb
}

// AutoMigrate runs schema migrations for both app and staff realms.
func AutoMigrate(gdb *gorm.DB) error {
	if err := gdb.AutoMigrate(AutoMigrateModels()...); err != nil {
		return err
	}
	if err := MigrateVerification(gdb); err != nil {
		return err
	}
	return MigrateAccounts(gdb)
}

// AutoMigrateModels is the authoritative table inventory used by migrations and
// the privacy registry coverage test.
func AutoMigrateModels() []interface{} {
	return []interface{}{
		&models.User{},
		&models.DriverProfile{},
		&models.PassengerProfile{},
		&models.Rating{},
		&models.StaffUser{},
		&models.StaffSession{},
		&models.StaffAuditLog{},
		&models.DriverDocument{},
		&models.VerificationReview{},
		&models.DriverDuplicateFlag{},
		&models.DriverNotification{},
		&models.UserStatusEvent{}, &models.SupportNote{}, &models.PhoneChallenge{},
		&models.PromoCode{}, &models.PromoAudience{}, &models.PromoRedemption{},
		&models.ReferralSettings{}, &models.UserReferralCode{}, &models.Referral{},
		&models.RideCompletionReceipt{}, &models.ReferralRewardPhone{},
		&models.AccountDeletionRequest{}, &models.DataExportRequest{},
		&models.Ride{}, &models.UserBlock{}, &models.Report{}, &models.ReportNote{}, &models.AccountFlag{}, &models.NotificationDelivery{},
		&models.UserDevice{}, &models.RideLocationPoint{}, &models.FraudFlag{}, &models.FraudRule{}, &models.FraudAction{}, &models.FraudNote{}, &models.FraudMitigation{}, &models.FraudEvent{},
		&models.Wallet{}, &models.LedgerEntry{}, &models.PaymentTransaction{}, &models.CommissionRule{}, &models.PaymentSettings{}, &models.DriverPayout{}, &models.Refund{}, &models.WebhookEvent{}, &models.ReconciliationRun{}, &models.ReconciliationException{},
		&models.EmergencyContact{}, &models.SOSAlert{}, &models.SOSLocationPing{}, &models.SOSEvent{}, &models.ShareToken{}, &models.IncidentReport{}, &models.IncidentAttachment{}, &models.IncidentNote{},
	}
}
