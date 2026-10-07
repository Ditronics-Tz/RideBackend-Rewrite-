package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"ride-backend/config"
	"ride-backend/db"
	"ride-backend/models"
	"ride-backend/store"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type LegacyUser struct {
	ID           string
	Name         string
	Email        string
	PasswordHash string
	Role         string
	IsActive     bool
}

func main() {
	dryRun := flag.Bool("dry-run", false, "Simulate migration without modifying the database")
	flag.Parse()

	cfg := config.LoadConfig()
	gdb := db.Connect(cfg)
	appStore := store.New(gdb)

	fmt.Printf("=== Staff Migration Tool ===\n")
	if *dryRun {
		fmt.Printf("Mode: DRY RUN (no changes will be applied)\n\n")
	} else {
		fmt.Printf("Mode: LIVE MIGRATION\n\n")
	}

	// Find all legacy users with role admin, support, or mzee
	var legacyUsers []LegacyUser
	err := gdb.Table("users").
		Where("LOWER(role) IN ?", []string{"admin", "support", "mzee"}).
		Order("created_at ASC").
		Find(&legacyUsers).Error
	if err != nil {
		log.Fatalf("Failed to query legacy users: %v", err)
	}

	totalFound := len(legacyUsers)
	migratedCount := 0
	skippedCount := 0
	errorCount := 0

	for _, u := range legacyUsers {
		email := strings.ToLower(strings.TrimSpace(u.Email))
		var targetRole models.StaffRole
		switch strings.ToLower(u.Role) {
		case "mzee", "admin":
			targetRole = models.StaffRoleAdmin
		case "support":
			targetRole = models.StaffRoleSupport
		default:
			fmt.Printf("Skipping user %s: unrecognized legacy role %s\n", email, u.Role)
			errorCount++
			continue
		}

		// Check if staff user already exists
		existingStaff, exists := appStore.GetStaffUserByEmail(email)
		if exists {
			fmt.Printf("Staff account for %s already exists (Role: %s).", email, existingStaff.Role)
			if !*dryRun {
				_ = appStore.DisableAppUser(u.ID)
				fmt.Printf(" Ensured app realm user is disabled.\n")
			} else {
				fmt.Printf(" Would disable app realm user.\n")
			}
			skippedCount++
			continue
		}

		if *dryRun {
			fmt.Printf("[Dry Run] Would migrate %s (%s) from legacy role %s -> staff role %s (must_change_password=true, disable in app realm)\n",
				u.Name, email, u.Role, targetRole)
			migratedCount++
			continue
		}

		// Perform live migration in a transaction
		txErr := gdb.Transaction(func(tx *gorm.DB) error {
			now := time.Now()
			newStaff := models.StaffUser{
				ID:                 uuid.NewString(),
				Name:               u.Name,
				Email:              email,
				PasswordHash:       u.PasswordHash, // Copy existing hash
				Role:               targetRole,
				IsActive:           true,
				MustChangePassword: true, // Must change password on first login
				FailedAttempts:     0,
				CreatedAt:          now,
				UpdatedAt:          now,
			}
			if err := tx.Create(&newStaff).Error; err != nil {
				return err
			}

			// Disable user in app realm
			if err := tx.Table("users").Where("id = ?", u.ID).Update("is_active", false).Error; err != nil {
				return err
			}
			return nil
		})

		if txErr != nil {
			fmt.Printf("Error migrating %s: %v\n", email, txErr)
			errorCount++
		} else {
			fmt.Printf("Successfully migrated %s -> staff_%s (app realm disabled)\n", email, targetRole)
			migratedCount++
		}
	}

	fmt.Printf("\n=== Migration Summary ===\n")
	fmt.Printf("Total Legacy Users Found: %d\n", totalFound)
	fmt.Printf("Successfully Migrated:    %d\n", migratedCount)
	fmt.Printf("Already Existing/Skipped: %d\n", skippedCount)
	fmt.Printf("Errors Encountered:       %d\n", errorCount)

	if errorCount > 0 {
		os.Exit(1)
	}
}
