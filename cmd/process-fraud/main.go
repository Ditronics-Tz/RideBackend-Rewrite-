package main

import (
	"flag"
	"log"
	"ride-backend/config"
	"ride-backend/db"
	"ride-backend/store"
)

func main() {
	nightly := flag.Bool("nightly", false, "queue a daily rule pass for every app account")
	flag.Parse()
	cfg := config.LoadConfig()
	s := store.New(db.Connect(cfg))
	s.ConfigureOperations(cfg)
	if *nightly {
		if err := s.QueueNightlyFraud(); err != nil {
			log.Fatal(err)
		}
	}
	if err := s.ProcessFraud(); err != nil {
		log.Fatal(err)
	}
}
