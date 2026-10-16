-- +goose Up
CREATE TABLE app_notifications (
  id uuid PRIMARY KEY,
  user_id uuid NOT NULL,
  kind varchar(24) NOT NULL,
  title varchar(160) NOT NULL,
  body varchar(500) NOT NULL,
  data jsonb NOT NULL DEFAULT '{}'::jsonb,
  read_at timestamptz,
  created_at timestamptz NOT NULL
);
CREATE INDEX idx_app_notifications_user_created ON app_notifications (user_id, created_at DESC, id DESC);
CREATE INDEX idx_app_notifications_unread ON app_notifications (user_id, created_at DESC) WHERE read_at IS NULL;

CREATE TABLE push_devices (
  id uuid PRIMARY KEY,
  user_id uuid NOT NULL,
  session_family_id uuid NOT NULL,
  token_hash varchar(64) NOT NULL UNIQUE,
  token_ciphertext text NOT NULL,
  platform varchar(16) NOT NULL,
  active boolean NOT NULL DEFAULT true,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL
);
CREATE INDEX idx_push_devices_user ON push_devices (user_id) WHERE active = true;
CREATE INDEX idx_push_devices_family ON push_devices (session_family_id);

CREATE TABLE push_deliveries (
  notification_id uuid NOT NULL REFERENCES app_notifications(id) ON DELETE CASCADE,
  device_id uuid NOT NULL REFERENCES push_devices(id) ON DELETE CASCADE,
  status varchar(16) NOT NULL,
  attempts bigint NOT NULL DEFAULT 0,
  claimed_at timestamptz,
  next_attempt_at timestamptz NOT NULL,
  sent_at timestamptz,
  PRIMARY KEY (notification_id, device_id)
);
CREATE INDEX idx_push_deliveries_pending ON push_deliveries (next_attempt_at) WHERE status = 'pending';

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Notification history is retained; rollback requires a reviewed archival plan'; END $$;
-- +goose StatementEnd
