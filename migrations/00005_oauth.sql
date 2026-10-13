-- +goose Up
CREATE TABLE IF NOT EXISTS oauth_states (
  state_hash varchar(64) PRIMARY KEY,
  browser_hash varchar(64) NOT NULL,
  redirect_uri text NOT NULL,
  expires_at timestamptz NOT NULL,
  created_at timestamptz
);
CREATE INDEX IF NOT EXISTS idx_oauth_states_expires_at ON oauth_states (expires_at);
CREATE TABLE IF NOT EXISTS oauth_identities (
  id uuid PRIMARY KEY,
  provider text NOT NULL,
  subject text NOT NULL,
  user_id uuid NOT NULL,
  created_at timestamptz
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_oauth_provider_subject ON oauth_identities (provider, subject);
CREATE INDEX IF NOT EXISTS idx_oauth_identities_user_id ON oauth_identities (user_id);

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Auth rollback requires a reviewed session/data migration plan'; END $$;
-- +goose StatementEnd
