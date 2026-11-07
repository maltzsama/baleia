package migrations

import (
	"context"

	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(up00007, down00007)
}

const up00007Postgres = `
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
`

const up00007MySQL = `CREATE TABLE migrations (id                          CHAR(36) PRIMARY KEY DEFAULT (UUID()),
    created_by                  CHAR(36)          NOT NULL,
    name                        TEXT        NOT NULL,
    source_type                 TEXT        NOT NULL,
    source_url                  TEXT        NOT NULL,
    source_credentials_enc      TEXT,
    target_catalog_connection_id CHAR(36)         NOT NULL,
    status                      TEXT        NOT NULL DEFAULT ('pending'),
    total_tables                INTEGER          NOT NULL DEFAULT 0,
    migrated_tables             INTEGER          NOT NULL DEFAULT 0,
    failed_tables               INTEGER          NOT NULL DEFAULT 0,
    temporal_workflow_id        TEXT,
    started_at                  DATETIME(6),
    finished_at                 DATETIME(6),
    created_at                  DATETIME(6)   NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    CONSTRAINT migrations_created_by_fkey FOREIGN KEY (created_by) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT migrations_source_type_check CHECK (source_type IN ('nessie')),
    CONSTRAINT migrations_target_catalog_connection_id_fkey FOREIGN KEY (target_catalog_connection_id) REFERENCES catalog_connections (id) ON DELETE RESTRICT,
    CONSTRAINT migrations_status_check CHECK (status IN ('pending', 'running', 'finished', 'failed', 'cancelled'))) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
CREATE TABLE migration_items (id                CHAR(36) PRIMARY KEY DEFAULT (UUID()),
    migration_id      CHAR(36)          NOT NULL,
    source_namespace  TEXT        NOT NULL,
    source_table      TEXT        NOT NULL,
    target_namespace  TEXT        NOT NULL,
    target_table      TEXT        NOT NULL,
    status            TEXT        NOT NULL DEFAULT ('pending'),
    error_message     TEXT,
    started_at        DATETIME(6),
    finished_at       DATETIME(6),
    CONSTRAINT migration_items_migration_id_fkey FOREIGN KEY (migration_id) REFERENCES migrations (id) ON DELETE CASCADE,
    CONSTRAINT migration_items_status_check CHECK (status IN ('pending', 'running', 'finished', 'failed', 'skipped'))) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
CREATE INDEX IF NOT EXISTS idx_migrations_created_by      ON migrations (created_by);
CREATE INDEX IF NOT EXISTS idx_migrations_status          ON migrations (status(255));
CREATE INDEX IF NOT EXISTS idx_migration_items_migration  ON migration_items (migration_id);
CREATE INDEX IF NOT EXISTS idx_migration_items_status     ON migration_items (status(255));`

const down00007Postgres = `
DROP TABLE IF EXISTS migration_items;
DROP TABLE IF EXISTS migrations;
`

const down00007MySQL = `DROP TABLE IF EXISTS migration_items;
DROP TABLE IF EXISTS migrations;`

func up00007(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, up00007Postgres, up00007MySQL)
}

func down00007(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, down00007Postgres, down00007MySQL)
}
