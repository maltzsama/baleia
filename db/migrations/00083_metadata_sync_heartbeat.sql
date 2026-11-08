-- +goose Up
-- A crashed metadata sync is taken over once its heartbeat goes stale instead
-- of waiting on an advisory lock (ADR 0020).
ALTER TABLE metadata_sync_runs ADD COLUMN heartbeat_at TIMESTAMPTZ;

-- +goose Down
ALTER TABLE metadata_sync_runs DROP COLUMN heartbeat_at;
