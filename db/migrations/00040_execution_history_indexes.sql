-- +goose Up
-- Execution history (#36): a trigram index for substring search on SQL and a
-- composite index for the workspace's newest-first keyset pages.
CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE INDEX IF NOT EXISTS query_executions_sql_trgm
    ON query_executions USING gin (sql gin_trgm_ops);
CREATE INDEX IF NOT EXISTS query_executions_ws_started
    ON query_executions (workspace_id, started_at DESC, id DESC);

-- +goose Down
DROP INDEX IF EXISTS query_executions_ws_started;
DROP INDEX IF EXISTS query_executions_sql_trgm;
