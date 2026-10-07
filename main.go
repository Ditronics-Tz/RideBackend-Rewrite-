package main

import (
	"log"

	"ride-backend/config"
	"ride-backend/db"
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

	app := routes.BuildApp(cfg, appStore)

	log.Printf("Server starting on port %s...", cfg.Port)
	log.Fatal(app.Listen(":" + cfg.Port))
}
