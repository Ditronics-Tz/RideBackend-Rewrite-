-- B5 app sessions, security events, lockout and persistent rate buckets.
-- +goose Up
ALTER TABLE users ADD COLUMN IF NOT EXISTS failed_login_count bigint NOT NULL DEFAULT 0;
ALTER TABLE users ADD COLUMN IF NOT EXISTS locked_until timestamptz;
CREATE INDEX IF NOT EXISTS idx_users_locked_until ON users (locked_until);
ALTER TABLE staff_users ADD COLUMN IF NOT EXISTS auth_version bigint NOT NULL DEFAULT 0;
ALTER TABLE staff_users ADD COLUMN IF NOT EXISTS pending_totp_secret_enc text;
ALTER TABLE staff_users ADD COLUMN IF NOT EXISTS pending_totp_expires_at timestamptz;
CREATE TABLE IF NOT EXISTS "app_sessions" ("id" uuid,"user_id" uuid NOT NULL,"family_id" uuid NOT NULL,"token_hash" varchar(64) NOT NULL,"device_id" varchar(128),"platform" varchar(32),"ip" varchar(45),"created_at" timestamptz NOT NULL,"last_used_at" timestamptz NOT NULL,"expires_at" timestamptz NOT NULL,"revoked_at" timestamptz,"replaced_by" uuid,PRIMARY KEY ("id"));
CREATE INDEX IF NOT EXISTS "idx_app_sessions_revoked_at" ON "app_sessions" ("revoked_at");
CREATE UNIQUE INDEX IF NOT EXISTS "idx_app_sessions_token_hash" ON "app_sessions" ("token_hash");
CREATE INDEX IF NOT EXISTS "idx_app_sessions_family_id" ON "app_sessions" ("family_id");
CREATE INDEX IF NOT EXISTS "idx_app_session_user_expiry" ON "app_sessions" ("user_id","expires_at");
CREATE TABLE IF NOT EXISTS "app_security_events" ("id" uuid,"user_id" uuid NOT NULL,"type" varchar(64) NOT NULL,"ip" varchar(45),"created_at" timestamptz NOT NULL,PRIMARY KEY ("id"));
CREATE INDEX IF NOT EXISTS "idx_app_security_events_type" ON "app_security_events" ("type");
CREATE INDEX IF NOT EXISTS "idx_app_security_user_time" ON "app_security_events" ("user_id","created_at");
CREATE TABLE IF NOT EXISTS "auth_rate_buckets" ("scope" varchar(64),"key_hash" varchar(64),"count" bigint NOT NULL,"window_start" timestamptz NOT NULL,"expires_at" timestamptz NOT NULL,PRIMARY KEY ("scope","key_hash"));
CREATE INDEX IF NOT EXISTS "idx_auth_rate_buckets_expires_at" ON "auth_rate_buckets" ("expires_at");

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Authentication rollback is disabled; use a reviewed data migration'; END $$;
-- +goose StatementEnd
