package jobs

import (
	"context"
	"log/slog"
	"time"

	"ride-backend/config"
	"ride-backend/statuspage"
	"ride-backend/store"
)

// RunRideDispatch advances expired offers even when no client is polling.
func RunRideDispatch(ctx context.Context, s *store.Store, cfg *config.Config) {
	timer := time.NewTicker(5 * time.Second)
	defer timer.Stop()
	for {
		failed := false
		if err := s.WithContext(ctx).ExpireShared(); err != nil {
			slog.Error("Shared expiry job failed")
			failed = ctx.Err() == nil
		}
		if err := s.WithContext(ctx).RetryTransportCollections(); err != nil {
			slog.Error("Transport collection retry failed")
			failed = failed || ctx.Err() == nil
		}
		if err := s.DispatchDueScheduledRides(ctx, cfg); err != nil {
			slog.Error("Scheduled ride dispatch failed")
			failed = failed || ctx.Err() == nil
		}
		if err := s.AdvanceExpiredRideOffers(ctx, cfg.RideSearchRadiusKM, time.Duration(cfg.RideOfferTimeoutSeconds)*time.Second); err != nil {
			slog.Error("Ride dispatch expiry job failed")
			failed = failed || ctx.Err() == nil
		}
		if ctx.Err() == nil {
			if failed {
				statuspage.Fail(statuspage.WorkerDispatch, "Ride dispatch tick failed")
			} else {
				statuspage.Succeed(statuspage.WorkerDispatch)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
	}
}
