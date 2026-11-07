package migrations

import (
	"context"

	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(up00006, down00006)
}

const up00006Postgres = `
CREATE TABLE retrievers (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id     UUID          NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    created_by       UUID          NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    name             TEXT        NOT NULL,
    definition_sql   TEXT          NOT NULL,
    dest_catalog     TEXT        NOT NULL,
    dest_namespace   TEXT        NOT NULL,
    dest_table       TEXT        NOT NULL,
    cron_schedule    TEXT        NOT NULL,
    refresh_type     TEXT        NOT NULL DEFAULT 'incremental' CHECK (refresh_type IN ('incremental', 'full')),
    status           TEXT        NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'paused', 'error')),
    last_refresh_at  TIMESTAMPTZ,
    next_refresh_at  TIMESTAMPTZ,
    created_at       TIMESTAMPTZ   NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ   NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, name)
);

CREATE TABLE retriever_refresh_history (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    retriever_id         UUID          NOT NULL REFERENCES retrievers (id) ON DELETE CASCADE,
    temporal_workflow_id TEXT        NOT NULL,
    refresh_type         TEXT        NOT NULL CHECK (refresh_type IN ('incremental', 'full')),
    status               TEXT        NOT NULL CHECK (status IN ('running', 'finished', 'failed')),
    rows_processed       BIGINT,
    duration_ms          BIGINT,
    error_message        TEXT,
    started_at           TIMESTAMPTZ   NOT NULL DEFAULT now(),
    finished_at          TIMESTAMPTZ
);

CREATE INDEX idx_retrievers_workspace         ON retrievers (workspace_id);
CREATE INDEX idx_retrievers_status            ON retrievers (status);
CREATE INDEX idx_retrievers_next_refresh_at   ON retrievers (next_refresh_at);
CREATE INDEX idx_retriever_refresh_retriever  ON retriever_refresh_history (retriever_id);
CREATE INDEX idx_retriever_refresh_status     ON retriever_refresh_history (status);
`

const up00006MySQL = `
CREATE TABLE retrievers (
    id               CHAR(36) PRIMARY KEY DEFAULT (UUID()),
    workspace_id     CHAR(36)          NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    created_by       CHAR(36)          NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    name             TEXT        NOT NULL,
    definition_sql   TEXT          NOT NULL,
    dest_catalog     TEXT        NOT NULL,
    dest_namespace   TEXT        NOT NULL,
    dest_table       TEXT        NOT NULL,
    cron_schedule    TEXT        NOT NULL,
    refresh_type     TEXT        NOT NULL DEFAULT 'incremental' CHECK (refresh_type IN ('incremental', 'full')),
    status           TEXT        NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'paused', 'error')),
    last_refresh_at  DATETIME(6),
    next_refresh_at  DATETIME(6),
    created_at       DATETIME(6)   NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at       DATETIME(6)   NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    UNIQUE (workspace_id, name)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE retriever_refresh_history (
    id                   CHAR(36) PRIMARY KEY DEFAULT (UUID()),
    retriever_id         CHAR(36)          NOT NULL REFERENCES retrievers (id) ON DELETE CASCADE,
    temporal_workflow_id TEXT        NOT NULL,
    refresh_type         TEXT        NOT NULL CHECK (refresh_type IN ('incremental', 'full')),
    status               TEXT        NOT NULL CHECK (status IN ('running', 'finished', 'failed')),
    rows_processed       BIGINT,
    duration_ms          BIGINT,
    error_message        TEXT,
    started_at           DATETIME(6)   NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    finished_at          DATETIME(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE INDEX idx_retrievers_workspace         ON retrievers (workspace_id);
CREATE INDEX idx_retrievers_status            ON retrievers (status);
CREATE INDEX idx_retrievers_next_refresh_at   ON retrievers (next_refresh_at);
CREATE INDEX idx_retriever_refresh_retriever  ON retriever_refresh_history (retriever_id);
CREATE INDEX idx_retriever_refresh_status     ON retriever_refresh_history (status);
`

const down00006Postgres = `
DROP TABLE IF EXISTS retriever_refresh_history;
DROP TABLE IF EXISTS retrievers;
`

const down00006MySQL = `
DROP TABLE IF EXISTS retriever_refresh_history;
DROP TABLE IF EXISTS retrievers;
`

func up00006(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, up00006Postgres, up00006MySQL)
}

func down00006(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, down00006Postgres, down00006MySQL)
}
