-- +goose Up
-- Existing names remain untouched. New email-only accounts may omit a name.
ALTER TABLE users ALTER COLUMN name DROP NOT NULL;

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Restoring NOT NULL requires a reviewed backfill for incomplete profiles'; END $$;
-- +goose StatementEnd
