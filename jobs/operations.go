package jobs

import (
	"context"
	"log"
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
			log.Printf("Safety job failed: %v", err)
		}
		if err := s.ProcessFraud(); err != nil {
			log.Printf("Fraud job failed: %v", err)
		}
		if err := s.ProcessPayments(); err != nil {
			log.Printf("Payment job failed: %v", err)
		}
		if _, err := s.Reconcile("daily:" + time.Now().UTC().Format("2006-01-02")); err != nil {
			log.Printf("Reconciliation queue failed: %v", err)
		}
		if err := s.ProcessReconciliation(); err != nil {
			log.Printf("Reconciliation job failed: %v", err)
		}
		day := time.Now().UTC().Format("2006-01-02")
		if lastFraudDay != day {
			if err := s.QueueNightlyFraud(); err != nil {
				log.Printf("Fraud queue failed: %v", err)
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
