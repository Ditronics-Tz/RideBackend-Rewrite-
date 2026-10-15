-- +goose Up
ALTER TABLE staff_users ADD COLUMN IF NOT EXISTS last_accepted_totp_step bigint;
ALTER TABLE staff_users ADD COLUMN IF NOT EXISTS totp_verified_at timestamptz;
CREATE TABLE IF NOT EXISTS staff_mfa_challenges (
  id uuid PRIMARY KEY,
  staff_id uuid NOT NULL,
  purpose varchar(16) NOT NULL,
  expires_at timestamptz NOT NULL,
  consumed_at timestamptz,
  failed_attempts bigint NOT NULL DEFAULT 0,
  created_at timestamptz NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_staff_mfa_challenges_staff_id ON staff_mfa_challenges (staff_id);
CREATE INDEX IF NOT EXISTS idx_staff_mfa_challenges_expires_at ON staff_mfa_challenges (expires_at);
CREATE TABLE IF NOT EXISTS staff_mfa_recovery_codes (
  id uuid PRIMARY KEY,
  staff_id uuid NOT NULL,
  code_hash varchar(64) NOT NULL,
  used_at timestamptz,
  created_at timestamptz NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_staff_mfa_recovery_codes_staff_id ON staff_mfa_recovery_codes (staff_id);

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Staff MFA rollback requires a reviewed credential migration'; END $$;
-- +goose StatementEnd
