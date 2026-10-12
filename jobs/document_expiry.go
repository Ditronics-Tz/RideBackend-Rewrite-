package jobs

import (
	"context"
	"log"
	"ride-backend/config"
	"ride-backend/store"
	"time"
)

// RunDocumentExpiry catches up on startup, then runs at midnight Tanzania time.
// Database row locks and unique notification keys make overlapping workers safe.
func RunDocumentExpiry(ctx context.Context, s *store.Store) {
	zone := time.FixedZone("EAT", 3*3600)
	for {
		if err := s.ExpireDriverDocuments(time.Now().UTC(), ""); err != nil {
			log.Printf("Document expiry sweep failed: %v", err)
		}
		now := time.Now().In(zone)
		next := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, zone)
		timer := time.NewTimer(time.Until(next))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

// RunDataRequests performs an immediate catch-up and then polls every five minutes.
func RunDataRequests(ctx context.Context, s *store.Store, cfg *config.Config) {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		if err := s.ProcessDataRequests(cfg); err != nil {
			log.Printf("Data request processing failed: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
