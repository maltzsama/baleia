-- +goose Up
-- A run can fail before any statement runs — e.g. the admission queue timed
-- out or was full (#399). It carries an error code with no statement to hold
-- it, so the execution itself records it.
ALTER TABLE query_executions ADD COLUMN error_code TEXT;

-- +goose Down
ALTER TABLE query_executions DROP COLUMN IF EXISTS error_code;
