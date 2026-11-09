package jobs

import (
	"context"
	"log/slog"
	"ride-backend/config"
	"ride-backend/statuspage"
	"ride-backend/store"
	"time"
)

func RunOperations(ctx context.Context, s *store.Store, cfg *config.Config) {
	timer := time.NewTicker(10 * time.Second)
	defer timer.Stop()
	lastFraudDay := ""
	for {
		failed := false
		if err := s.ProcessSafety(cfg); err != nil {
			slog.Error("Safety job failed")
			failed = ctx.Err() == nil
		}
		if err := s.ProcessFraud(); err != nil {
			slog.Error("Fraud job failed")
			failed = failed || ctx.Err() == nil
		}
		if err := s.ProcessPayments(); err != nil {
			slog.Error("Payment job failed")
			failed = failed || ctx.Err() == nil
		}
		if _, err := s.Reconcile("daily:" + time.Now().UTC().Format("2006-01-02")); err != nil {
			slog.Error("Reconciliation queue failed")
			failed = failed || ctx.Err() == nil
		}
		if err := s.ProcessReconciliation(); err != nil {
			slog.Error("Reconciliation job failed")
			failed = failed || ctx.Err() == nil
		}
		day := time.Now().UTC().Format("2006-01-02")
		if lastFraudDay != day {
			if err := s.QueueNightlyFraud(); err != nil {
				slog.Error("Fraud queue failed")
				failed = failed || ctx.Err() == nil
			} else {
				lastFraudDay = day
			}
		}
		if ctx.Err() == nil {
			if failed {
				statuspage.Fail(statuspage.WorkerOperations, "A payments, safety or fraud tick failed")
			} else {
				statuspage.Succeed(statuspage.WorkerOperations)
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
			statuspage.Fail(statuspage.WorkerPush, "Push delivery tick failed")
		} else if ctx.Err() == nil {
			statuspage.Succeed(statuspage.WorkerPush)
		}
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
	}
}
