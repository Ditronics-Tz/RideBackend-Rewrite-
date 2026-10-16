package jobs

import (
	"context"
	"log/slog"
	"ride-backend/config"
	"ride-backend/store"
	"time"
)

func RunOperations(ctx context.Context, s *store.Store, cfg *config.Config) {
	timer := time.NewTicker(10 * time.Second)
	defer timer.Stop()
	lastFraudDay := ""
	for {
		if err := s.ProcessSafety(cfg); err != nil {
			slog.Error("Safety job failed")
		}
		if err := s.ProcessFraud(); err != nil {
			slog.Error("Fraud job failed")
		}
		if err := s.ProcessPayments(); err != nil {
			slog.Error("Payment job failed")
		}
		if _, err := s.Reconcile("daily:" + time.Now().UTC().Format("2006-01-02")); err != nil {
			slog.Error("Reconciliation queue failed")
		}
		if err := s.ProcessReconciliation(); err != nil {
			slog.Error("Reconciliation job failed")
		}
		day := time.Now().UTC().Format("2006-01-02")
		if lastFraudDay != day {
			if err := s.QueueNightlyFraud(); err != nil {
				slog.Error("Fraud queue failed")
			} else {
				lastFraudDay = day
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
	}
}

// RunPushDeliveries is independent of the optional safety/finance worker flag.
// The durable inbox remains available if this worker or FCM is unavailable.
func RunPushDeliveries(ctx context.Context, s *store.Store) {
	timer := time.NewTicker(10 * time.Second)
	defer timer.Stop()
	for {
		if err := s.ProcessPushDeliveries(ctx); err != nil && ctx.Err() == nil {
			slog.Error("Push delivery job failed")
		}
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
	}
}
