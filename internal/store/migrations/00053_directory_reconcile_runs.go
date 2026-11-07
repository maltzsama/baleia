package migrations

import (
	"context"

	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(up00053, down00053)
}

const up00053Postgres = `
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
`


const up00053MySQL = `
CREATE TABLE directory_reconcile_runs (
    id           CHAR(36)        PRIMARY KEY DEFAULT (UUID()),
    provider_id  CHAR(36)        NOT NULL REFERENCES auth_providers (id) ON DELETE CASCADE,
    started_at   DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    finished_at  DATETIME(6),
    outcome      TEXT        CHECK (outcome IN ('success', 'error', 'aborted_safety')),
    seen         INTEGER     NOT NULL DEFAULT 0,
    suspended    INTEGER     NOT NULL DEFAULT 0,
    reinstated   INTEGER     NOT NULL DEFAULT 0,
    error        TEXT,
    actions      JSON       NOT NULL DEFAULT '[]'
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE INDEX idx_directory_reconcile_runs_provider
    ON directory_reconcile_runs (provider_id, started_at DESC);
`


const down00053Postgres = `
DROP TABLE IF EXISTS directory_reconcile_runs;
`


const down00053MySQL = `
DROP TABLE IF EXISTS directory_reconcile_runs;
`


func up00053(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, up00053Postgres, up00053MySQL)
}

func down00053(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, down00053Postgres, down00053MySQL)
}
