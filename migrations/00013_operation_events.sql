-- +goose Up
CREATE TABLE IF NOT EXISTS operation_events (
  id uuid PRIMARY KEY,
  actor_type varchar(16) NOT NULL,
  actor_id varchar(64) NOT NULL DEFAULT '',
  action varchar(64) NOT NULL,
  method varchar(8) NOT NULL,
  route varchar(160) NOT NULL,
  status bigint NOT NULL,
  request_id varchar(64) NOT NULL,
  target_id varchar(64) NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_operation_events_created_at ON operation_events (created_at DESC);
CREATE INDEX IF NOT EXISTS idx_operation_events_actor_id ON operation_events (actor_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_operation_events_action ON operation_events (action, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_operation_events_request_id ON operation_events (request_id);
CREATE INDEX IF NOT EXISTS idx_operation_events_target_id ON operation_events (target_id);

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Operation history is retained; rollback requires a reviewed archival plan'; END $$;
-- +goose StatementEnd
