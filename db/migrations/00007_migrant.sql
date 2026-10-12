-- +goose Up

CREATE TABLE migrations (
    id                          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    created_by                  UUID          NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    name                        TEXT        NOT NULL,
    source_type                 TEXT        NOT NULL CHECK (source_type IN ('nessie')),
    source_url                  TEXT        NOT NULL,
    source_credentials_enc      TEXT,
    target_catalog_connection_id UUID         NOT NULL REFERENCES catalog_connections (id) ON DELETE RESTRICT,
    status                      TEXT        NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'running', 'finished', 'failed', 'cancelled')),
    total_tables                INTEGER          NOT NULL DEFAULT 0,
    migrated_tables             INTEGER          NOT NULL DEFAULT 0,
    failed_tables               INTEGER          NOT NULL DEFAULT 0,
    temporal_workflow_id        TEXT,
    started_at                  TIMESTAMPTZ,
    finished_at                 TIMESTAMPTZ,
    created_at                  TIMESTAMPTZ   NOT NULL DEFAULT now()
);

CREATE TABLE migration_items (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    migration_id      UUID          NOT NULL REFERENCES migrations (id) ON DELETE CASCADE,
    source_namespace  TEXT        NOT NULL,
    source_table      TEXT        NOT NULL,
    target_namespace  TEXT        NOT NULL,
    target_table      TEXT        NOT NULL,
    status            TEXT        NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'running', 'finished', 'failed', 'skipped')),
    error_message     TEXT,
    started_at        TIMESTAMPTZ,
    finished_at       TIMESTAMPTZ
);

CREATE INDEX idx_migrations_created_by      ON migrations (created_by);
CREATE INDEX idx_migrations_status          ON migrations (status);
CREATE INDEX idx_migration_items_migration  ON migration_items (migration_id);
CREATE INDEX idx_migration_items_status     ON migration_items (status);

-- +goose Down

DROP TABLE IF EXISTS migration_items;
DROP TABLE IF EXISTS migrations;
