-- +goose Up
ALTER TABLE app_sessions ADD COLUMN IF NOT EXISTS absolute_expires_at timestamptz;
UPDATE app_sessions SET absolute_expires_at = created_at + interval '365 days' WHERE absolute_expires_at IS NULL;
ALTER TABLE app_sessions ALTER COLUMN absolute_expires_at SET NOT NULL;
CREATE TABLE IF NOT EXISTS google_challenges (
  id uuid PRIMARY KEY,
  nonce_hash varchar(64) NOT NULL,
  expires_at timestamptz NOT NULL,
  created_at timestamptz NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_google_challenges_expires_at ON google_challenges (expires_at);

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Mobile authentication rollback requires a reviewed session migration'; END $$;
-- +goose StatementEnd
