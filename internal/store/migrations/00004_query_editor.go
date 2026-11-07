package migrations

import (
	"context"

	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(up00004, down00004)
}

const up00004Postgres = `
CREATE TABLE query_folders (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID          NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    parent_id    UUID          REFERENCES query_folders (id) ON DELETE CASCADE,
    name         TEXT        NOT NULL,
    created_at   TIMESTAMPTZ   NOT NULL DEFAULT now()
);

CREATE TABLE saved_queries (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID          NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    folder_id    UUID          REFERENCES query_folders (id) ON DELETE SET NULL,
    created_by   UUID          NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    name         TEXT        NOT NULL,
    sql          TEXT          NOT NULL,
    created_at   TIMESTAMPTZ   NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ   NOT NULL DEFAULT now()
);

CREATE TABLE query_executions (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id    UUID          NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    user_id         UUID          NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    saved_query_id  UUID          REFERENCES saved_queries (id) ON DELETE SET NULL,
    status          TEXT        NOT NULL CHECK (status IN ('running', 'finished', 'failed', 'cancelled')),
    sql             TEXT          NOT NULL,
    trino_query_id  TEXT,
    rows_returned   BIGINT,
    duration_ms     BIGINT,
    started_at      TIMESTAMPTZ   NOT NULL DEFAULT now(),
    finished_at     TIMESTAMPTZ
);

CREATE TABLE query_statements (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    execution_id    UUID          NOT NULL REFERENCES query_executions (id) ON DELETE CASCADE,
    position        INTEGER          NOT NULL,
    sql             TEXT          NOT NULL,
    status          TEXT        NOT NULL CHECK (status IN ('running', 'finished', 'failed', 'cancelled')),
    trino_query_id  TEXT,
    rows_returned   BIGINT,
    duration_ms     BIGINT,
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
`


const up00004MySQL = `
CREATE TABLE query_folders (
    id           CHAR(36) PRIMARY KEY DEFAULT (UUID()),
    workspace_id CHAR(36)          NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    parent_id    CHAR(36)          REFERENCES query_folders (id) ON DELETE CASCADE,
    name         TEXT        NOT NULL,
    created_at   DATETIME(6)   NOT NULL DEFAULT CURRENT_TIMESTAMP(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE saved_queries (
    id           CHAR(36) PRIMARY KEY DEFAULT (UUID()),
    workspace_id CHAR(36)          NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    folder_id    CHAR(36)          REFERENCES query_folders (id) ON DELETE SET NULL,
    created_by   CHAR(36)          NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    name         TEXT        NOT NULL,
    sql          TEXT          NOT NULL,
    created_at   DATETIME(6)   NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at   DATETIME(6)   NOT NULL DEFAULT CURRENT_TIMESTAMP(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE query_executions (
    id              CHAR(36) PRIMARY KEY DEFAULT (UUID()),
    workspace_id    CHAR(36)          NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    user_id         CHAR(36)          NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    saved_query_id  CHAR(36)          REFERENCES saved_queries (id) ON DELETE SET NULL,
    status          TEXT        NOT NULL CHECK (status IN ('running', 'finished', 'failed', 'cancelled')),
    sql             TEXT          NOT NULL,
    trino_query_id  TEXT,
    rows_returned   BIGINT,
    duration_ms     BIGINT,
    started_at      DATETIME(6)   NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    finished_at     DATETIME(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE query_statements (
    id              CHAR(36) PRIMARY KEY DEFAULT (UUID()),
    execution_id    CHAR(36)          NOT NULL REFERENCES query_executions (id) ON DELETE CASCADE,
    position        INTEGER          NOT NULL,
    sql             TEXT          NOT NULL,
    status          TEXT        NOT NULL CHECK (status IN ('running', 'finished', 'failed', 'cancelled')),
    trino_query_id  TEXT,
    rows_returned   BIGINT,
    duration_ms     BIGINT,
    error_message   TEXT,
    started_at      DATETIME(6)   NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    finished_at     DATETIME(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE INDEX idx_query_folders_workspace    ON query_folders (workspace_id);
CREATE INDEX idx_query_folders_parent       ON query_folders (parent_id);
CREATE INDEX idx_saved_queries_workspace    ON saved_queries (workspace_id);
CREATE INDEX idx_saved_queries_folder       ON saved_queries (folder_id);
CREATE INDEX idx_saved_queries_created_by   ON saved_queries (created_by);
CREATE INDEX idx_query_executions_workspace ON query_executions (workspace_id);
CREATE INDEX idx_query_executions_user      ON query_executions (user_id);
CREATE INDEX idx_query_executions_status    ON query_executions (status);
CREATE INDEX idx_query_statements_execution ON query_statements (execution_id);
`


const down00004Postgres = `
DROP TABLE IF EXISTS query_statements;
DROP TABLE IF EXISTS query_executions;
DROP TABLE IF EXISTS saved_queries;
DROP TABLE IF EXISTS query_folders;
`


const down00004MySQL = `
DROP TABLE IF EXISTS query_statements;
DROP TABLE IF EXISTS query_executions;
DROP TABLE IF EXISTS saved_queries;
DROP TABLE IF EXISTS query_folders;
`


func up00004(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, up00004Postgres, up00004MySQL)
}

func down00004(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, down00004Postgres, down00004MySQL)
}
