package migrations

import (
	"context"

	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(up00032, down00032)
}

const up00032Postgres = `
-- Physical metadata snapshots (#15): namespaces, tables and columns per
-- logical catalog, filled by the metadata sync. Keyed by catalog, never by
-- workspace: grants scope them at read time (#18). Rows not seen by a pass
-- are tombstoned (removed_at), not deleted; the sync purges tombstones
-- older than 7 days.

-- pg_trgm is a trusted extension: CREATE on the database is enough.
CREATE EXTENSION IF NOT EXISTS pg_trgm;

CREATE TABLE metadata_namespaces (
    catalog_id    UUID        NOT NULL REFERENCES catalogs (id) ON DELETE CASCADE,
    namespace     TEXT[]      NOT NULL,
    last_seen_at  TIMESTAMPTZ NOT NULL,
    removed_at    TIMESTAMPTZ,
    PRIMARY KEY (catalog_id, namespace)
);

CREATE TABLE metadata_tables (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    catalog_id       UUID        NOT NULL REFERENCES catalogs (id) ON DELETE CASCADE,
    namespace        TEXT[]      NOT NULL,
    name             TEXT        NOT NULL,
    kind             TEXT        NOT NULL CHECK (kind IN ('table', 'view', 'materialized_view')),
    -- Iceberg: the metadata-location; databases and engines: md5 of the
    -- ordered column list. A changed fingerprint reloads the columns.
    fingerprint      TEXT        NOT NULL,
    -- format, location, current_snapshot_id, etag, … (what the source knows)
    properties       JSONB       NOT NULL DEFAULT '{}'::jsonb,
    comment          TEXT,
    last_seen_at     TIMESTAMPTZ NOT NULL,
    last_changed_at  TIMESTAMPTZ NOT NULL,
    removed_at       TIMESTAMPTZ,
    UNIQUE (catalog_id, namespace, name)
);
CREATE INDEX metadata_tables_name_trgm ON metadata_tables USING gin (name gin_trgm_ops) WHERE removed_at IS NULL;

CREATE TABLE metadata_columns (
    table_id     UUID    NOT NULL REFERENCES metadata_tables (id) ON DELETE CASCADE,
    ordinal      INT     NOT NULL,
    name         TEXT    NOT NULL,
    -- The type as the source reports it, in its own type system.
    native_type  TEXT    NOT NULL,
    type_system  TEXT    NOT NULL,
    nullable     BOOLEAN,
    comment      TEXT,
    PRIMARY KEY (table_id, ordinal)
);
CREATE INDEX metadata_columns_name_trgm ON metadata_columns USING gin (name gin_trgm_ops);

CREATE TABLE metadata_sync_runs (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    catalog_id      UUID        NOT NULL REFERENCES catalogs (id) ON DELETE CASCADE,
    started_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at     TIMESTAMPTZ,
    status          TEXT        NOT NULL CHECK (status IN ('running', 'finished', 'failed')),
    tables_seen     INT,
    tables_changed  INT,
    tables_removed  INT,
    error_code      TEXT,
    error_message   TEXT,
    -- provider: the catalog's own source (catalog manager, database driver);
    -- engine: introspection through one of the catalog's bindings.
    source_path     TEXT        NOT NULL CHECK (source_path IN ('provider', 'engine'))
);
CREATE INDEX metadata_sync_runs_catalog ON metadata_sync_runs (catalog_id, started_at DESC);
`


const up00032MySQL = `
CREATE TABLE metadata_namespaces (
    catalog_id    CHAR(36)        NOT NULL REFERENCES catalogs (id) ON DELETE CASCADE,
    namespace     JSON      NOT NULL,
    last_seen_at  DATETIME(6) NOT NULL,
    removed_at    DATETIME(6),
    PRIMARY KEY (catalog_id, namespace)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE metadata_tables (
    id               CHAR(36) PRIMARY KEY DEFAULT (UUID()),
    catalog_id       CHAR(36)        NOT NULL REFERENCES catalogs (id) ON DELETE CASCADE,
    namespace        JSON      NOT NULL,
    name             TEXT        NOT NULL,
    kind             TEXT        NOT NULL CHECK (kind IN ('table', 'view', 'materialized_view')),
    
    
    fingerprint      TEXT        NOT NULL,
    
    properties       JSON       NOT NULL DEFAULT '{}',
    comment          TEXT,
    last_seen_at     DATETIME(6) NOT NULL,
    last_changed_at  DATETIME(6) NOT NULL,
    removed_at       DATETIME(6),
    UNIQUE (catalog_id, namespace, name)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE metadata_columns (
    table_id     CHAR(36)    NOT NULL REFERENCES metadata_tables (id) ON DELETE CASCADE,
    ordinal      INT     NOT NULL,
    name         TEXT    NOT NULL,
    
    native_type  TEXT    NOT NULL,
    type_system  TEXT    NOT NULL,
    nullable     BOOLEAN,
    comment      TEXT,
    PRIMARY KEY (table_id, ordinal)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE metadata_sync_runs (
    id              CHAR(36) PRIMARY KEY DEFAULT (UUID()),
    catalog_id      CHAR(36)        NOT NULL REFERENCES catalogs (id) ON DELETE CASCADE,
    started_at      DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    finished_at     DATETIME(6),
    status          TEXT        NOT NULL CHECK (status IN ('running', 'finished', 'failed')),
    tables_seen     INT,
    tables_changed  INT,
    tables_removed  INT,
    error_code      TEXT,
    error_message   TEXT,
    
    
    source_path     TEXT        NOT NULL CHECK (source_path IN ('provider', 'engine'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
CREATE INDEX metadata_sync_runs_catalog ON metadata_sync_runs (catalog_id, started_at DESC);
`


const down00032Postgres = `
DROP TABLE IF EXISTS metadata_sync_runs;
DROP TABLE IF EXISTS metadata_columns;
DROP TABLE IF EXISTS metadata_tables;
DROP TABLE IF EXISTS metadata_namespaces;
`


const down00032MySQL = `
DROP TABLE IF EXISTS metadata_sync_runs;
DROP TABLE IF EXISTS metadata_columns;
DROP TABLE IF EXISTS metadata_tables;
DROP TABLE IF EXISTS metadata_namespaces;
`


func up00032(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, up00032Postgres, up00032MySQL)
}

func down00032(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, down00032Postgres, down00032MySQL)
}
