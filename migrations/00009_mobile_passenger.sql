-- Mobile passenger data, scheduled dispatch, and support conversation.
-- +goose Up
CREATE TABLE IF NOT EXISTS saved_places (
  id uuid PRIMARY KEY,
  user_id uuid NOT NULL,
  kind varchar(16) NOT NULL,
  label varchar(80) NOT NULL,
  address varchar(250) NOT NULL,
  lat numeric NOT NULL,
  lng numeric NOT NULL,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_saved_places_user_id ON saved_places (user_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_saved_places_home_work ON saved_places (user_id, kind) WHERE kind IN ('home', 'work');

CREATE TABLE IF NOT EXISTS passenger_preferences (
  user_id uuid PRIMARY KEY,
  quiet_ride boolean NOT NULL,
  extra_pickup_time boolean NOT NULL,
  share_trip_status boolean NOT NULL,
  precise_location boolean NOT NULL,
  ride_updates boolean NOT NULL,
  trip_messages boolean NOT NULL,
  promotions boolean NOT NULL,
  email_enabled boolean NOT NULL,
  sms_enabled boolean NOT NULL,
  push_enabled boolean NOT NULL,
  language varchar(8) NOT NULL,
  updated_at timestamptz NOT NULL
);

CREATE TABLE IF NOT EXISTS scheduled_rides (
  id uuid PRIMARY KEY,
  passenger_id uuid NOT NULL,
  pickup_address varchar(250) NOT NULL,
  pickup_lat numeric NOT NULL,
  pickup_lng numeric NOT NULL,
  dropoff_address varchar(250) NOT NULL,
  dropoff_lat numeric NOT NULL,
  dropoff_lng numeric NOT NULL,
  vehicle_type varchar(50) NOT NULL,
  scheduled_for timestamptz NOT NULL,
  status varchar(24) NOT NULL,
  ride_id uuid,
  failure_code varchar(40) NOT NULL,
  request_key varchar(100) NOT NULL,
  request_hash varchar(64) NOT NULL,
  claimed_until timestamptz,
  attempt_count bigint NOT NULL,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_scheduled_rides_passenger_id ON scheduled_rides (passenger_id);
CREATE INDEX IF NOT EXISTS idx_scheduled_rides_scheduled_for ON scheduled_rides (scheduled_for);
CREATE INDEX IF NOT EXISTS idx_scheduled_rides_status ON scheduled_rides (status);
CREATE UNIQUE INDEX IF NOT EXISTS idx_scheduled_request ON scheduled_rides (passenger_id, request_key);

CREATE TABLE IF NOT EXISTS support_ticket_messages (
  id uuid PRIMARY KEY,
  ticket_id uuid NOT NULL,
  actor_id uuid NOT NULL,
  actor_type varchar(16) NOT NULL,
  body text NOT NULL,
  request_key varchar(100) NOT NULL,
  request_hash varchar(64) NOT NULL,
  created_at timestamptz NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_support_ticket_messages_ticket_id ON support_ticket_messages (ticket_id, created_at);
CREATE UNIQUE INDEX IF NOT EXISTS idx_support_message_request ON support_ticket_messages (ticket_id, actor_id, request_key);

CREATE TABLE IF NOT EXISTS support_ticket_attachments (
  id uuid PRIMARY KEY,
  ticket_id uuid NOT NULL,
  uploader_id uuid NOT NULL,
  actor_type varchar(16) NOT NULL,
  object_key text NOT NULL,
  request_key varchar(100) NOT NULL,
  request_hash varchar(64) NOT NULL,
  content_type varchar(80) NOT NULL,
  size bigint NOT NULL,
  created_at timestamptz NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_support_ticket_attachments_ticket_id ON support_ticket_attachments (ticket_id, created_at);
CREATE UNIQUE INDEX IF NOT EXISTS idx_support_attachment_request ON support_ticket_attachments (ticket_id, uploader_id, request_key);

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Passenger mobile rollback is disabled; preserve saved places, schedules, preferences and support history'; END $$;
-- +goose StatementEnd
