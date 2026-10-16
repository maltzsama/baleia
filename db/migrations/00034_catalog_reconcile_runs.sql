-- +goose Up

-- Binding reconciliation runs per federated engine (#37). Only the last 100
-- runs of an engine are kept: each insert prunes the older ones.
CREATE TABLE catalog_reconcile_runs (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    engine_id    UUID        NOT NULL REFERENCES engines (id) ON DELETE CASCADE,
    started_at   TIMESTAMPTZ NOT NULL,
    finished_at  TIMESTAMPTZ NOT NULL,
    dry_run      BOOLEAN     NOT NULL,
    -- [{catalog, kind, reason, result, error_code}]
    actions      JSONB       NOT NULL DEFAULT '[]'::jsonb,
    adopted      TEXT[]      NOT NULL DEFAULT '{}',
    error        TEXT
);
CREATE INDEX catalog_reconcile_runs_engine ON catalog_reconcile_runs (engine_id, started_at DESC);

-- +goose Down

DROP TABLE IF EXISTS catalog_reconcile_runs;
