-- +goose Up

CREATE TABLE lenses (
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    catalog_connection_id UUID          NOT NULL REFERENCES catalog_connections (id) ON DELETE RESTRICT,
    created_by            UUID          NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    name                  TEXT        NOT NULL UNIQUE,
    iceberg_catalog       TEXT        NOT NULL,
    iceberg_namespace     TEXT        NOT NULL,
    iceberg_table         TEXT        NOT NULL,
    cron_schedule         TEXT        NOT NULL,
    expire_snapshots      BOOLEAN          NOT NULL DEFAULT true,
    remove_orphan_files   BOOLEAN          NOT NULL DEFAULT true,
    compact_data_files    BOOLEAN          NOT NULL DEFAULT true,
    rewrite_manifests     BOOLEAN          NOT NULL DEFAULT true,
    remove_old_metadata   BOOLEAN          NOT NULL DEFAULT true,
    status                TEXT        NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'paused', 'error')),
    last_run_at           TIMESTAMPTZ,
    next_run_at           TIMESTAMPTZ,
    created_at            TIMESTAMPTZ   NOT NULL DEFAULT now(),
    updated_at            TIMESTAMPTZ   NOT NULL DEFAULT now()
);

CREATE TABLE lens_run_history (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    lens_id              UUID          NOT NULL REFERENCES lenses (id) ON DELETE CASCADE,
    temporal_workflow_id TEXT        NOT NULL,
    status               TEXT        NOT NULL CHECK (status IN ('running', 'finished', 'failed')),
    operations_summary   TEXT,
    error_message        TEXT,
    duration_ms          BIGINT,
    started_at           TIMESTAMPTZ   NOT NULL DEFAULT now(),
    finished_at          TIMESTAMPTZ
);

CREATE INDEX idx_lenses_catalog_connection ON lenses (catalog_connection_id);
CREATE INDEX idx_lenses_status             ON lenses (status);
CREATE INDEX idx_lenses_next_run_at        ON lenses (next_run_at);
CREATE INDEX idx_lens_run_history_lens_id  ON lens_run_history (lens_id);
CREATE INDEX idx_lens_run_history_status   ON lens_run_history (status);

-- +goose Down

DROP TABLE IF EXISTS lens_run_history;
DROP TABLE IF EXISTS lenses;
