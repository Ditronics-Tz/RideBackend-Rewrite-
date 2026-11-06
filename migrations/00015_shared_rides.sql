-- +goose Up

CREATE TABLE transport_settings (
  id bigint NOT NULL,
  version bigint NOT NULL,
  enabled boolean NOT NULL,
  shared_fee bigint NOT NULL,
  hail_percent bigint NOT NULL,
  PRIMARY KEY (id)
);

CREATE TABLE shared_trips (
  id uuid NOT NULL,
  driver_id uuid NOT NULL,
  completed bigint NOT NULL DEFAULT 0,
  pickup_lat double precision NOT NULL,
  pickup_lng double precision NOT NULL,
  pickup_address text NOT NULL,
  dropoff_lat double precision NOT NULL,
  dropoff_lng double precision NOT NULL,
  dropoff_address text NOT NULL,
  vehicle text NOT NULL,
  capacity bigint NOT NULL,
  seats bigint NOT NULL,
  price bigint NOT NULL,
  fee bigint NOT NULL,
  status text NOT NULL,
  deadline bigint NOT NULL,
  created bigint NOT NULL,
  PRIMARY KEY (id)
);

CREATE TABLE shared_seats (
  id uuid NOT NULL,
  trip_id uuid NOT NULL,
  passenger_id uuid NOT NULL,
  status text NOT NULL,
  expires bigint NOT NULL,
  failures bigint NOT NULL,
  PRIMARY KEY (id),
  FOREIGN KEY (trip_id) REFERENCES shared_trips(id)
);

CREATE TABLE shared_requests (
  actor uuid NOT NULL,
  key text NOT NULL,
  hash text NOT NULL,
  trip_id uuid NOT NULL,
  PRIMARY KEY (actor, key)
);

CREATE INDEX shared_nearby ON shared_trips(status, pickup_lat, pickup_lng, deadline);

CREATE UNIQUE INDEX shared_driver_active ON shared_trips(driver_id) WHERE status IN ('waiting','in_progress');

CREATE INDEX shared_seat_trip ON shared_seats(trip_id, status, expires);

CREATE INDEX shared_seat_passenger ON shared_seats(passenger_id, trip_id);

INSERT INTO transport_settings VALUES (1, 0, false, 0, 0);

ALTER TABLE rides ADD COLUMN hail_percent bigint;
ALTER TABLE rides ADD COLUMN assessed_fee bigint;
ALTER TABLE driver_profiles ADD COLUMN passenger_seats bigint NOT NULL DEFAULT 0;

-- +goose Down

-- +goose StatementBegin

DO $$ BEGIN RAISE EXCEPTION 'Archive transport and settlement records before rollback'; END $$;

-- +goose StatementEnd
