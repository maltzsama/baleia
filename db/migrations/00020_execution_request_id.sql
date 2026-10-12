-- +goose Up
-- Correlation: keep the HTTP X-Request-ID on the execution row so a query can
-- be traced from the request log through the Temporal workflow and back.
ALTER TABLE query_executions ADD COLUMN request_id TEXT;

-- +goose Down
ALTER TABLE query_executions DROP COLUMN request_id;
