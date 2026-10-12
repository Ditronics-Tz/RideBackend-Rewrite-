package main

import (
	"log"

	"ride-backend/config"
	"ride-backend/db"
	"ride-backend/store"
)

func main() {
	cfg := config.LoadConfig()
	s := store.New(db.Connect(cfg))
	if err := s.ProcessDataRequests(cfg); err != nil {
		log.Printf("Data request sweep failed: %v", err)
		log.Fatal("data request sweep incomplete")
	}
}
