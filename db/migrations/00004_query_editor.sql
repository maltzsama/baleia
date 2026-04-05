-- +goose Up

CREATE TABLE query_folders (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID          NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    parent_id    UUID          REFERENCES query_folders (id) ON DELETE CASCADE,
    name         STRING        NOT NULL,
    created_at   TIMESTAMPTZ   NOT NULL DEFAULT now()
);

CREATE TABLE saved_queries (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID          NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    folder_id    UUID          REFERENCES query_folders (id) ON DELETE SET NULL,
    created_by   UUID          NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    name         STRING        NOT NULL,
    sql          TEXT          NOT NULL,
    created_at   TIMESTAMPTZ   NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ   NOT NULL DEFAULT now()
);

CREATE TABLE query_executions (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id    UUID          NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    user_id         UUID          NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    saved_query_id  UUID          REFERENCES saved_queries (id) ON DELETE SET NULL,
    status          STRING        NOT NULL CHECK (status IN ('running', 'finished', 'failed', 'cancelled')),
    sql             TEXT          NOT NULL,
    trino_query_id  STRING,
    rows_returned   INT8,
    duration_ms     INT8,
    started_at      TIMESTAMPTZ   NOT NULL DEFAULT now(),
    finished_at     TIMESTAMPTZ
);

CREATE TABLE query_statements (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    execution_id    UUID          NOT NULL REFERENCES query_executions (id) ON DELETE CASCADE,
    position        INT4          NOT NULL,
    sql             TEXT          NOT NULL,
    status          STRING        NOT NULL CHECK (status IN ('running', 'finished', 'failed', 'cancelled')),
    trino_query_id  STRING,
    rows_returned   INT8,
    duration_ms     INT8,
    error_message   TEXT,
    started_at      TIMESTAMPTZ   NOT NULL DEFAULT now(),
    finished_at     TIMESTAMPTZ
);

CREATE INDEX idx_query_folders_workspace    ON query_folders (workspace_id);
CREATE INDEX idx_query_folders_parent       ON query_folders (parent_id);
CREATE INDEX idx_saved_queries_workspace    ON saved_queries (workspace_id);
CREATE INDEX idx_saved_queries_folder       ON saved_queries (folder_id);
CREATE INDEX idx_saved_queries_created_by   ON saved_queries (created_by);
CREATE INDEX idx_query_executions_workspace ON query_executions (workspace_id);
CREATE INDEX idx_query_executions_user      ON query_executions (user_id);
CREATE INDEX idx_query_executions_status    ON query_executions (status);
CREATE INDEX idx_query_statements_execution ON query_statements (execution_id);

-- +goose Down

DROP TABLE IF EXISTS query_statements;
DROP TABLE IF EXISTS query_executions;
DROP TABLE IF EXISTS saved_queries;
DROP TABLE IF EXISTS query_folders;
