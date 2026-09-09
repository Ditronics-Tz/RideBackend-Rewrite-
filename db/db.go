package db

import (
	"log"

	"ride-backend/config"
	"ride-backend/models"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Connect opens the Postgres connection and runs auto-migrations.
// It creates tables for users, driver/passenger profiles and ratings
// if they don't exist yet.
func Connect(cfg *config.Config) *gorm.DB {
	gdb, err := gorm.Open(postgres.Open(cfg.DSN()), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		log.Fatalf("Failed to connect to Postgres (%s:%s/%s as %s): %v",
			cfg.DBHost, cfg.DBPort, cfg.DBName, cfg.DBUser, err)
	}

	if err := gdb.AutoMigrate(
		&models.User{},
		&models.DriverProfile{},
		&models.PassengerProfile{},
		&models.Rating{},
	); err != nil {
		log.Fatalf("Failed to run database migrations: %v", err)
	}

	log.Println("Connected to Postgres and migrations are up to date")
	return gdb
}
