-- +goose Up
ALTER TABLE shared_trips ADD COLUMN version bigint NOT NULL DEFAULT 0;
ALTER TABLE shared_trips ADD COLUMN fee_version bigint NOT NULL DEFAULT 0;
ALTER TABLE shared_seats ADD COLUMN created bigint NOT NULL DEFAULT 0;
ALTER TABLE shared_seats ADD COLUMN confirmed_at bigint NOT NULL DEFAULT 0;
ALTER TABLE shared_seats ADD COLUMN completed_at bigint NOT NULL DEFAULT 0;
ALTER TABLE rides ADD COLUMN fee_version bigint NOT NULL DEFAULT 0;
CREATE TABLE transport_cursors (id bigint PRIMARY KEY, revision bigint NOT NULL DEFAULT 0);
INSERT INTO transport_cursors VALUES (1,0);
CREATE TABLE transport_events (
 id bigint PRIMARY KEY, trip_id uuid NOT NULL REFERENCES shared_trips(id), version bigint NOT NULL,
 type text NOT NULL, actor_id uuid NOT NULL, at bigint NOT NULL, reason text NOT NULL DEFAULT ''
);
CREATE INDEX transport_event_trip ON transport_events(trip_id,id);
CREATE TABLE transport_assessments (
 mode text NOT NULL, trip_id uuid NOT NULL, driver_id uuid NOT NULL,
 gross bigint NOT NULL, fee bigint NOT NULL, net bigint NOT NULL,
 passengers bigint NOT NULL, fee_version bigint NOT NULL, status text NOT NULL,
 created bigint NOT NULL, updated bigint NOT NULL,
 PRIMARY KEY(mode,trip_id), CHECK(mode IN ('shared','hail')),
 CHECK(fee>=0), CHECK(gross>=0), CHECK(net=gross-fee)
);
CREATE INDEX transport_assessment_pending ON transport_assessments(status,created);
CREATE INDEX transport_assessment_driver ON transport_assessments(driver_id,created);

ALTER TABLE support_tickets ADD COLUMN shared_trip_id uuid;
ALTER TABLE sos_alerts ADD COLUMN shared_trip_id uuid;
CREATE INDEX support_shared_trip ON support_tickets(shared_trip_id);
CREATE INDEX sos_shared_trip ON sos_alerts(shared_trip_id);

ALTER TABLE ratings ADD COLUMN shared_trip_id uuid;
CREATE UNIQUE INDEX idx_shared_rating ON ratings(shared_trip_id,from_user_id,to_user_id) WHERE shared_trip_id IS NOT NULL;

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Retain transport history and assessments during application rollback'; END $$;
-- +goose StatementEnd
