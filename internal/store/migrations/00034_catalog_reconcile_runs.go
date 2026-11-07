package migrations

import (
	"context"

	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(up00034, down00034)
}

const up00034Postgres = `
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
`

const up00034MySQL = `
CREATE TABLE catalog_reconcile_runs (
    id           CHAR(36) PRIMARY KEY DEFAULT (UUID()),
    engine_id    CHAR(36)        NOT NULL REFERENCES engines (id) ON DELETE CASCADE,
    started_at   DATETIME(6) NOT NULL,
    finished_at  DATETIME(6) NOT NULL,
    dry_run      BOOLEAN     NOT NULL,
    
    actions      JSON       NOT NULL DEFAULT '[]',
    adopted      JSON      NOT NULL DEFAULT '{}',
    error        TEXT
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
CREATE INDEX catalog_reconcile_runs_engine ON catalog_reconcile_runs (engine_id, started_at DESC);
`

const down00034Postgres = `
DROP TABLE IF EXISTS catalog_reconcile_runs;
`

const down00034MySQL = `
DROP TABLE IF EXISTS catalog_reconcile_runs;
`

func up00034(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, up00034Postgres, up00034MySQL)
}

func down00034(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, down00034Postgres, down00034MySQL)
}
