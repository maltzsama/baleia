-- +goose Up
-- Optional cost routing for Trino (#550). When enabled, classification runs
-- EXPLAIN (FORMAT JSON) for a SELECT and moves a run whose estimate exceeds
-- the threshold into the heavy class.
ALTER TABLE engines
    ADD COLUMN cost_routing_enabled BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN cost_threshold_bytes BIGINT
        CHECK (cost_threshold_bytes IS NULL OR cost_threshold_bytes > 0);

-- +goose Down
ALTER TABLE engines
    DROP COLUMN IF EXISTS cost_threshold_bytes,
    DROP COLUMN IF EXISTS cost_routing_enabled;
