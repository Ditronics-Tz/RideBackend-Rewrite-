package main

import (
	"flag"
	"log"
	"ride-backend/config"
	"ride-backend/db"
	"ride-backend/store"
	"time"
)

func main() {
	reconcile := flag.Bool("reconcile", false, "queue the daily provider and ledger reconciliation")
	flag.Parse()
	cfg := config.LoadConfig()
	s := store.New(db.Connect(cfg))
	s.ConfigureOperations(cfg)
	if err := s.ProcessPayments(); err != nil {
		log.Fatal(err)
	}
	if *reconcile {
		if _, err := s.Reconcile("daily:" + time.Now().UTC().Format("2006-01-02")); err != nil {
			log.Fatal(err)
		}
	}
	if err := s.ProcessReconciliation(); err != nil {
		log.Fatal(err)
	}
}
