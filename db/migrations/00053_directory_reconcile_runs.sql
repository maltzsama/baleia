-- +goose Up

-- One row per directory reconciliation pass (#257). The run is append-only:
-- it records what the pass saw and changed, including a safety-brake abort.
CREATE TABLE directory_reconcile_runs (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    provider_id  UUID        NOT NULL REFERENCES auth_providers (id) ON DELETE CASCADE,
    started_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at  TIMESTAMPTZ,
    outcome      TEXT        CHECK (outcome IN ('success', 'error', 'aborted_safety')),
    seen         INTEGER     NOT NULL DEFAULT 0,
    suspended    INTEGER     NOT NULL DEFAULT 0,
    reinstated   INTEGER     NOT NULL DEFAULT 0,
    error        TEXT,
    actions      JSONB       NOT NULL DEFAULT '[]'::jsonb
);

CREATE INDEX idx_directory_reconcile_runs_provider
    ON directory_reconcile_runs (provider_id, started_at DESC);

-- +goose Down

DROP TABLE IF EXISTS directory_reconcile_runs;
