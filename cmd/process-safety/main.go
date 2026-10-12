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
	if err := s.ProcessSafety(cfg); err != nil {
		log.Fatal(err)
	}
}
