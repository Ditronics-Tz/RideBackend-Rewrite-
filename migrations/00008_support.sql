-- B8: persisted support tickets, ride-bound ratings, and rating moderation.
-- +goose Up
ALTER TABLE ratings ADD COLUMN IF NOT EXISTS ride_id uuid;
ALTER TABLE ratings ADD COLUMN IF NOT EXISTS hidden_at timestamptz;
ALTER TABLE ratings ADD COLUMN IF NOT EXISTS hidden_by uuid;
CREATE UNIQUE INDEX IF NOT EXISTS idx_rating_ride_rater ON ratings (ride_id, from_user_id) WHERE ride_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_rating_visible_to ON ratings (to_user_id, created_at DESC) WHERE hidden_at IS NULL;

CREATE TABLE IF NOT EXISTS support_tickets (
  id uuid PRIMARY KEY,
  user_id uuid NOT NULL,
  subject varchar(160) NOT NULL,
  description text NOT NULL,
  category varchar(40) NOT NULL,
  status varchar(24) NOT NULL,
  priority varchar(24) NOT NULL,
  assigned_to uuid,
  resolution_outcome text NOT NULL,
  resolution_note text NOT NULL,
  resolved_by uuid,
  resolved_at timestamptz,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_support_tickets_user_id ON support_tickets (user_id);
CREATE INDEX IF NOT EXISTS idx_support_tickets_category ON support_tickets (category);
CREATE INDEX IF NOT EXISTS idx_support_tickets_status ON support_tickets (status);
CREATE INDEX IF NOT EXISTS idx_support_tickets_priority ON support_tickets (priority);
CREATE INDEX IF NOT EXISTS idx_support_tickets_assigned_to ON support_tickets (assigned_to);
CREATE INDEX IF NOT EXISTS idx_support_tickets_created_at ON support_tickets (created_at);

CREATE TABLE IF NOT EXISTS support_ticket_notes (
  id uuid PRIMARY KEY,
  ticket_id uuid NOT NULL,
  staff_id uuid NOT NULL,
  note text NOT NULL,
  created_at timestamptz NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_support_ticket_notes_ticket_id ON support_ticket_notes (ticket_id);

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Support workflow rollback is disabled; preserve moderation and ticket history'; END $$;
-- +goose StatementEnd
