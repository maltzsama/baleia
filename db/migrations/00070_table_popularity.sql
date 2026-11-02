-- +goose Up
-- Popular tables per workspace (B7, #562): a scheduled pass counts the reads
-- of the last 30 days from statement_references with a 7-day half-life decay.
-- The counts rank search and completions and feed the explorer's "popular"
-- list; they are materialized so a read never scans the history.
CREATE TABLE table_popularity (
    workspace_id UUID             NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    catalog_id   UUID             NOT NULL REFERENCES catalogs (id) ON DELETE CASCADE,
    -- The one namespace level statement_references stores (the schema).
    namespace    TEXT             NOT NULL DEFAULT '',
    table_name   TEXT             NOT NULL,
    uses         BIGINT           NOT NULL DEFAULT 0,
    score        DOUBLE PRECISION NOT NULL DEFAULT 0,
    last_used_at TIMESTAMPTZ      NOT NULL,
    computed_at  TIMESTAMPTZ      NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, catalog_id, namespace, table_name)
);

CREATE INDEX table_popularity_workspace ON table_popularity (workspace_id, score DESC);

-- +goose Down
DROP TABLE IF EXISTS table_popularity;
