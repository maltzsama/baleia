package migrations

import (
	"context"

	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(up00011, down00011)
}

const up00011Postgres = `
CREATE TABLE trino_query_snapshots (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    trino_query_id       TEXT NOT NULL UNIQUE,
    execution_id         UUID REFERENCES query_executions(id) ON DELETE SET NULL,
    origin               TEXT NOT NULL CHECK (origin IN ('baleia', 'trino_direct')),
    "user"               TEXT NOT NULL,
    state                TEXT NOT NULL CHECK (state IN ('running', 'finished', 'failed', 'cancelled')),
    sql                  TEXT NOT NULL,
    catalog              TEXT,
    "schema"             TEXT,
    created_at           TIMESTAMPTZ NOT NULL,
    elapsed_ms           INTEGER,
    cpu_ms               INTEGER,
    physical_input_bytes BIGINT,
    output_rows          BIGINT,
    error_message        TEXT,
    raw_trino_payload    JSONB,
    first_seen_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_trino_query_snapshots_query_id ON trino_query_snapshots(trino_query_id);
CREATE INDEX idx_trino_query_snapshots_origin   ON trino_query_snapshots(origin);
CREATE INDEX idx_trino_query_snapshots_state    ON trino_query_snapshots(state);
CREATE INDEX idx_trino_query_snapshots_user     ON trino_query_snapshots("user");
CREATE INDEX idx_trino_query_snapshots_created  ON trino_query_snapshots(created_at);

CREATE TABLE cluster_snapshots (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    captured_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    active_workers   INTEGER NOT NULL DEFAULT 0,
    running_queries  INTEGER NOT NULL DEFAULT 0,
    queued_queries   INTEGER NOT NULL DEFAULT 0,
    blocked_queries  INTEGER NOT NULL DEFAULT 0,
    total_memory_bytes  BIGINT,
    free_memory_bytes   BIGINT,
    raw_payload      JSONB
);

CREATE INDEX idx_cluster_snapshots_captured ON cluster_snapshots(captured_at);

CREATE TABLE table_health_snapshots (
    id                     UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    catalog_connection_id  UUID NOT NULL REFERENCES catalog_connections(id) ON DELETE CASCADE,
    catalog                TEXT NOT NULL,
    namespace              TEXT NOT NULL,
    table_name             TEXT NOT NULL,
    captured_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    snapshot_count         INTEGER NOT NULL DEFAULT 0,
    file_count             INTEGER,
    total_size_bytes       BIGINT,
    oldest_snapshot_age_hours INTEGER,
    last_maintenance_at    TIMESTAMPTZ,
    health_score           INTEGER NOT NULL DEFAULT 100 CHECK (health_score >= 0 AND health_score <= 100),
    needs_compaction       BOOLEAN NOT NULL DEFAULT false,
    needs_snapshot_expiry  BOOLEAN NOT NULL DEFAULT false,
    needs_orphan_cleanup   BOOLEAN NOT NULL DEFAULT false,
    UNIQUE(catalog_connection_id, catalog, namespace, table_name)
);

CREATE INDEX idx_table_health_conn     ON table_health_snapshots(catalog_connection_id);
CREATE INDEX idx_table_health_captured ON table_health_snapshots(captured_at);
`

const up00011MySQL = `CREATE TABLE trino_query_snapshots (id                   CHAR(36) PRIMARY KEY DEFAULT (UUID()),
    trino_query_id       TEXT NOT NULL,
    execution_id         CHAR(36),
    origin               TEXT NOT NULL,
    {{bt}}user{{bt}}               TEXT NOT NULL,
    state                TEXT NOT NULL,
    {{bt}}sql{{bt}}                  TEXT NOT NULL,
    catalog              TEXT,
    {{bt}}schema{{bt}}             TEXT,
    created_at           DATETIME(6) NOT NULL,
    elapsed_ms           INTEGER,
    cpu_ms               INTEGER,
    physical_input_bytes BIGINT,
    output_rows          BIGINT,
    error_message        TEXT,
    raw_trino_payload    JSON,
    first_seen_at        DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    last_seen_at         DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    CONSTRAINT trino_query_snapshots_trino_query_id_key UNIQUE (trino_query_id(255)),
    CONSTRAINT trino_query_snapshots_execution_id_fkey FOREIGN KEY (execution_id) REFERENCES query_executions(id) ON DELETE SET NULL,
    CONSTRAINT trino_query_snapshots_origin_check CHECK (origin IN ('baleia', 'trino_direct')),
    CONSTRAINT trino_query_snapshots_state_check CHECK (state IN ('running', 'finished', 'failed', 'cancelled'))) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
CREATE INDEX IF NOT EXISTS idx_trino_query_snapshots_query_id ON trino_query_snapshots(trino_query_id(255));
CREATE INDEX IF NOT EXISTS idx_trino_query_snapshots_origin   ON trino_query_snapshots(origin(255));
CREATE INDEX IF NOT EXISTS idx_trino_query_snapshots_state    ON trino_query_snapshots(state(255));
CREATE INDEX IF NOT EXISTS idx_trino_query_snapshots_user     ON trino_query_snapshots({{bt}}user{{bt}}(255));
CREATE INDEX IF NOT EXISTS idx_trino_query_snapshots_created  ON trino_query_snapshots(created_at);
CREATE TABLE cluster_snapshots (id               CHAR(36) PRIMARY KEY DEFAULT (UUID()),
    captured_at      DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    active_workers   INTEGER NOT NULL DEFAULT 0,
    running_queries  INTEGER NOT NULL DEFAULT 0,
    queued_queries   INTEGER NOT NULL DEFAULT 0,
    blocked_queries  INTEGER NOT NULL DEFAULT 0,
    total_memory_bytes  BIGINT,
    free_memory_bytes   BIGINT,
    raw_payload      JSON) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
CREATE INDEX IF NOT EXISTS idx_cluster_snapshots_captured ON cluster_snapshots(captured_at);
CREATE TABLE table_health_snapshots (id                     CHAR(36) PRIMARY KEY DEFAULT (UUID()),
    catalog_connection_id  CHAR(36) NOT NULL,
    catalog                TEXT NOT NULL,
    namespace              TEXT NOT NULL,
    table_name             TEXT NOT NULL,
    captured_at            DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    snapshot_count         INTEGER NOT NULL DEFAULT 0,
    file_count             INTEGER,
    total_size_bytes       BIGINT,
    oldest_snapshot_age_hours INTEGER,
    last_maintenance_at    DATETIME(6),
    health_score           INTEGER NOT NULL DEFAULT 100,
    needs_compaction       BOOLEAN NOT NULL DEFAULT false,
    needs_snapshot_expiry  BOOLEAN NOT NULL DEFAULT false,
    needs_orphan_cleanup   BOOLEAN NOT NULL DEFAULT false,
    UNIQUE(catalog_connection_id, catalog(244), namespace(244), table_name(244)),
    CONSTRAINT table_health_snapshots_catalog_connection_id_fkey FOREIGN KEY (catalog_connection_id) REFERENCES catalog_connections(id) ON DELETE CASCADE,
    CONSTRAINT table_health_snapshots_health_score_check CHECK (health_score >= 0 AND health_score <= 100)) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
CREATE INDEX IF NOT EXISTS idx_table_health_conn     ON table_health_snapshots(catalog_connection_id);
CREATE INDEX IF NOT EXISTS idx_table_health_captured ON table_health_snapshots(captured_at);`

const down00011Postgres = `
DROP TABLE IF EXISTS table_health_snapshots;
DROP TABLE IF EXISTS cluster_snapshots;
DROP TABLE IF EXISTS trino_query_snapshots;
`

const down00011MySQL = `DROP TABLE IF EXISTS table_health_snapshots;
DROP TABLE IF EXISTS cluster_snapshots;
DROP TABLE IF EXISTS trino_query_snapshots;`

func up00011(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, up00011Postgres, up00011MySQL)
}

func down00011(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, down00011Postgres, down00011MySQL)
}
