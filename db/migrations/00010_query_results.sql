-- +goose Up

ALTER TABLE query_executions
    ADD COLUMN trino_catalog TEXT,
    ADD COLUMN result_key  TEXT;

-- +goose Down

ALTER TABLE query_executions
    DROP COLUMN IF EXISTS trino_catalog,
    DROP COLUMN IF EXISTS result_key;
