package migrations

import (
	"context"

	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(up00036, down00036)
}

const up00036Postgres = `
-- Export cursor for the audit trail (#50): the position (occurred_at, id) of
-- the last event a sink has accepted. One row per sink ('opensearch'). The
-- cursor only ever moves forward, and never past a failed item, so the export
-- is at-least-once, ordered and resumable. audit_events is never modified.
CREATE TABLE audit_export_cursor (
    sink        TEXT PRIMARY KEY,
    occurred_at TIMESTAMPTZ NOT NULL,
    id          BIGINT      NOT NULL,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
`

const up00036MySQL = `
CREATE TABLE audit_export_cursor (
    sink        TEXT PRIMARY KEY,
    occurred_at DATETIME(6) NOT NULL,
    id          BIGINT      NOT NULL,
    updated_at  DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
`

const down00036Postgres = `
DROP TABLE IF EXISTS audit_export_cursor;
`

const down00036MySQL = `
DROP TABLE IF EXISTS audit_export_cursor;
`

func up00036(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, up00036Postgres, up00036MySQL)
}

func down00036(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, down00036Postgres, down00036MySQL)
}
