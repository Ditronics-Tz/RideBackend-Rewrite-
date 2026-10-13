-- B7: persisted ride lifecycle, offers, driver locations, and transition keys.
-- +goose Up
ALTER TABLE rides ADD COLUMN IF NOT EXISTS estimated_fare bigint NOT NULL DEFAULT 0;
ALTER TABLE rides ADD COLUMN IF NOT EXISTS started_at timestamptz;
ALTER TABLE rides ADD COLUMN IF NOT EXISTS cancelled_at timestamptz;
ALTER TABLE rides ADD COLUMN IF NOT EXISTS offer_expires_at timestamptz;
ALTER TABLE rides ADD COLUMN IF NOT EXISTS cancel_reason text NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_rides_created_at ON rides (created_at DESC);
CREATE UNIQUE INDEX IF NOT EXISTS idx_active_passenger_ride ON rides (passenger_id)
WHERE status <> 'completed' AND status <> 'cancelled' AND status <> 'cancelled_by_passenger' AND status <> 'cancelled_by_driver'
    AND status <> 'cancelled_by_admin' AND status <> 'no_driver_found' AND status <> 'expired';
CREATE UNIQUE INDEX IF NOT EXISTS idx_active_driver_ride ON rides (driver_id)
  WHERE driver_id <> '00000000-0000-0000-0000-000000000000' AND status <> 'completed' AND status <> 'cancelled'
    AND status <> 'cancelled_by_passenger' AND status <> 'cancelled_by_driver'
    AND status <> 'cancelled_by_admin' AND status <> 'no_driver_found' AND status <> 'expired';
CREATE TABLE IF NOT EXISTS ride_events (
  id uuid PRIMARY KEY,
  ride_id uuid NOT NULL,
  type varchar(64) NOT NULL,
  actor_id uuid,
  actor_type varchar(16) NOT NULL,
  at timestamptz NOT NULL,
  meta text
);
CREATE INDEX IF NOT EXISTS idx_ride_events_timeline ON ride_events (ride_id, at);
CREATE INDEX IF NOT EXISTS idx_ride_events_actor_id ON ride_events (actor_id);
CREATE TABLE IF NOT EXISTS driver_locations (
  driver_id uuid PRIMARY KEY,
  lat decimal NOT NULL,
  lng decimal NOT NULL,
  accuracy decimal NOT NULL,
  updated_at timestamptz NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_driver_locations_updated_at ON driver_locations (updated_at);
CREATE TABLE IF NOT EXISTS ride_idempotency_keys (
  actor_id uuid NOT NULL,
  key varchar(100) NOT NULL,
  operation varchar(32) NOT NULL,
  request_hash varchar(64) NOT NULL,
  ride_id uuid NOT NULL,
  created_at timestamptz NOT NULL,
  PRIMARY KEY (actor_id, key)
);
CREATE INDEX IF NOT EXISTS idx_ride_idempotency_created_at ON ride_idempotency_keys (created_at);

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Ride lifecycle rollback is disabled; preserve event and payment history'; END $$;
-- +goose StatementEnd
