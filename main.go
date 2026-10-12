package main

import (
	"context"
	"log"

	"ride-backend/config"
	"ride-backend/db"
	"ride-backend/jobs"
	"ride-backend/routes"
	"ride-backend/store"
	"ride-backend/utils"
)

func main() {
	cfg := config.LoadConfig()

	// Ensure upload directories and migrate existing files
	utils.MigrateExistingUploads()

	// Postgres connection + auto-migrations
	gdb := db.Connect(cfg)
	appStore := store.New(gdb)
	go jobs.RunDocumentExpiry(context.Background(), appStore)
	if cfg.RunBackgroundJobs {
		go jobs.RunDataRequests(context.Background(), appStore, cfg)
	}

	app := routes.BuildApp(cfg, appStore)

	log.Printf("Server starting on port %s...", cfg.Port)
	log.Fatal(app.Listen(":" + cfg.Port))
}
