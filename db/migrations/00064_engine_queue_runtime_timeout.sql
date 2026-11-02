-- +goose Up
-- Runtime bound per workload class (#576): a run that waited up to 24h in the
-- queue must not be killed by the 30m global statement timeout. NULL keeps the
-- class default resolved by the API.
ALTER TABLE engine_queue_capacity
    ADD COLUMN runtime_timeout_ms BIGINT
    CHECK (runtime_timeout_ms IS NULL OR runtime_timeout_ms > 0);

-- +goose Down
ALTER TABLE engine_queue_capacity DROP COLUMN IF EXISTS runtime_timeout_ms;
