package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"ride-backend/config"
	"ride-backend/db"
	"ride-backend/jobs"
	"ride-backend/migrations"
	"ride-backend/routes"
	"ride-backend/store"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	if err := run(); err != nil {
		slog.Error("server stopped", "error", err.Error())
		os.Exit(1)
	}
}

func run() error {
	cfg := config.LoadConfig()
	gdb, err := db.Open(cfg)
	if err != nil {
		return err
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		return err
	}
	defer sqlDB.Close()
	sqlDB.SetMaxOpenConns(25)
	sqlDB.SetMaxIdleConns(5)
	sqlDB.SetConnMaxLifetime(30 * time.Minute)
	versionCtx, versionCancel := context.WithTimeout(context.Background(), 10*time.Second)
	version, versionErr := migrations.Version(versionCtx, sqlDB)
	versionCancel()
	if versionErr != nil || version != migrations.LatestVersion {
		return errors.New("database schema is not current; run cmd/migrate before starting the server")
	}
	appStore := store.New(gdb)
	if err := appStore.ConfigureStorage(cfg); err != nil {
		return err
	}
	if closer, ok := appStore.Storage.(interface{ Close() error }); ok {
		defer closer.Close()
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	app := routes.BuildApp(cfg, appStore)
	workerStore := appStore.WithContext(ctx)
	var workers sync.WaitGroup
	start := func(job func()) { workers.Add(1); go func() { defer workers.Done(); job() }() }
	start(func() { jobs.RunDocumentExpiry(ctx, workerStore) })
	if cfg.RunBackgroundJobs {
		start(func() { jobs.RunOperations(ctx, workerStore, cfg) })
		start(func() { jobs.RunDataRequests(ctx, workerStore, cfg) })
	}
	listenErr := make(chan error, 1)
	go func() { slog.Info("server starting", "port", cfg.Port); listenErr <- app.Listen(":" + cfg.Port) }()
	select {
	case err = <-listenErr:
		cancel()
	case <-ctx.Done():
		err = app.ShutdownWithTimeout(10 * time.Second)
	}
	finished := make(chan struct{})
	go func() { workers.Wait(); close(finished) }()
	select {
	case <-finished:
	case <-time.After(10 * time.Second):
		slog.Warn("worker shutdown timed out")
	}
	return err
}
