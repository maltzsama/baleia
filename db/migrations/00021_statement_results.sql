-- +goose Up
ALTER TABLE query_statements
    ADD COLUMN result_key   TEXT,
    ADD COLUMN result_bytes BIGINT,
    ADD COLUMN truncated    BOOLEAN NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE query_statements
    DROP COLUMN IF EXISTS truncated,
    DROP COLUMN IF EXISTS result_bytes,
    DROP COLUMN IF EXISTS result_key;