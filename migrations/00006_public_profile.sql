-- B6: publish only the explicitly selected public vehicle category.
-- +goose Up
ALTER TABLE driver_profiles ADD COLUMN IF NOT EXISTS vehicle_type varchar(50);

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Profile vehicle type rollback is disabled; review existing profile values first'; END $$;
-- +goose StatementEnd
