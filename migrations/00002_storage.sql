-- B3 storage fields are additive on databases previously managed by AutoMigrate.
-- +goose Up
ALTER TABLE driver_profiles ADD COLUMN IF NOT EXISTS profile_picture_key text;
ALTER TABLE passenger_profiles ADD COLUMN IF NOT EXISTS profile_picture_key text;
ALTER TABLE driver_documents ADD COLUMN IF NOT EXISTS object_key text;
ALTER TABLE driver_documents ADD COLUMN IF NOT EXISTS staging_object_key text;
ALTER TABLE driver_documents ADD COLUMN IF NOT EXISTS content_type text;
ALTER TABLE driver_documents ADD COLUMN IF NOT EXISTS expected_size bigint;
ALTER TABLE driver_documents ADD COLUMN IF NOT EXISTS upload_expires_at timestamptz;

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Storage rollback is disabled; migrate data with a reviewed plan'; END $$;
-- +goose StatementEnd
