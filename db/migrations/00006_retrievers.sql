-- +goose Up

CREATE TABLE retrievers (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id     UUID          NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    created_by       UUID          NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    name             STRING        NOT NULL,
    definition_sql   TEXT          NOT NULL,
    dest_catalog     STRING        NOT NULL,
    dest_namespace   STRING        NOT NULL,
    dest_table       STRING        NOT NULL,
    cron_schedule    STRING        NOT NULL,
    refresh_type     STRING        NOT NULL DEFAULT 'incremental' CHECK (refresh_type IN ('incremental', 'full')),
    status           STRING        NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'paused', 'error')),
    last_refresh_at  TIMESTAMPTZ,
    next_refresh_at  TIMESTAMPTZ,
    created_at       TIMESTAMPTZ   NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ   NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, name)
);

CREATE TABLE retriever_refresh_history (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    retriever_id         UUID          NOT NULL REFERENCES retrievers (id) ON DELETE CASCADE,
    temporal_workflow_id STRING        NOT NULL,
    refresh_type         STRING        NOT NULL CHECK (refresh_type IN ('incremental', 'full')),
    status               STRING        NOT NULL CHECK (status IN ('running', 'finished', 'failed')),
    rows_processed       INT8,
    duration_ms          INT8,
    error_message        TEXT,
    started_at           TIMESTAMPTZ   NOT NULL DEFAULT now(),
    finished_at          TIMESTAMPTZ
);

CREATE INDEX idx_retrievers_workspace         ON retrievers (workspace_id);
CREATE INDEX idx_retrievers_status            ON retrievers (status);
CREATE INDEX idx_retrievers_next_refresh_at   ON retrievers (next_refresh_at);
CREATE INDEX idx_retriever_refresh_retriever  ON retriever_refresh_history (retriever_id);
CREATE INDEX idx_retriever_refresh_status     ON retriever_refresh_history (status);

-- +goose Down

DROP TABLE IF EXISTS retriever_refresh_history;
DROP TABLE IF EXISTS retrievers;
