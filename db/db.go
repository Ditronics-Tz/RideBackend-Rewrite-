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

// Open creates the Postgres connection and runs auto-migrations, returning an error if it fails.
func Open(cfg *config.Config) (*gorm.DB, error) {
	gdb, err := gorm.Open(postgres.Open(cfg.DSN()), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Postgres (%s:%s/%s as %s): %w",
			cfg.DBHost, cfg.DBPort, cfg.DBName, cfg.DBUser, err)
	}

	if err := AutoMigrate(gdb); err != nil {
		return nil, fmt.Errorf("failed to run database migrations: %w", err)
	}

	return gdb, nil
}

// Connect opens the Postgres connection and logs fatally if connection fails.
func Connect(cfg *config.Config) *gorm.DB {
	gdb, err := Open(cfg)
	if err != nil {
		log.Fatalf("%v", err)
	}
	log.Println("Connected to Postgres and migrations are up to date")
	return gdb
}

// AutoMigrate runs schema migrations for both app and staff realms.
func AutoMigrate(gdb *gorm.DB) error {
	return gdb.AutoMigrate(
		&models.User{},
		&models.DriverProfile{},
		&models.PassengerProfile{},
		&models.Rating{},
		&models.StaffUser{},
		&models.StaffSession{},
		&models.StaffAuditLog{},
	)
}
