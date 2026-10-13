package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"ride-backend/config"
	"ride-backend/store"
)

func main() {
	dry := flag.Bool("dry-run", false, "inventory counts without changing files or database rows")
	root := flag.String("root", "./uploads", "upload directory")
	flag.Parse()
	if err := run(*root, *dry); err != nil {
		fmt.Fprintln(os.Stderr, "upload migration failed; no file names or database details are logged")
		os.Exit(1)
	}
}

func run(root string, dry bool) (runErr error) {
	// This command reads injected environment variables and does not load .env
	// or run startup schema migrations, including in dry-run mode.
	cfg, err := config.NewConfigFromEnv()
	if err != nil {
		return err
	}
	db, err := gorm.Open(postgres.Open(cfg.DSN()), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		return err
	}
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	defer func() {
		if err := sqlDB.Close(); runErr == nil {
			runErr = err
		}
	}()
	counts, err := store.New(db).MigrateLegacyUploads(root, dry)
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(counts)
}
