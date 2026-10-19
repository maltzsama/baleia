-- +goose Up

-- The owner an agent acts for (#61). NULL for human and service actors; set
-- for every event caused by an agent. ALTER TABLE on the partitioned parent
-- propagates to the existing partitions.
ALTER TABLE audit_events ADD COLUMN on_behalf_of UUID;

CREATE INDEX audit_events_on_behalf_of
    ON audit_events (on_behalf_of, occurred_at DESC)
    WHERE on_behalf_of IS NOT NULL;

-- +goose Down

DROP INDEX IF EXISTS audit_events_on_behalf_of;
ALTER TABLE audit_events DROP COLUMN IF EXISTS on_behalf_of;
