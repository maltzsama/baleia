package migrations

import (
	"context"

	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(up00010, down00010)
}

const up00010Postgres = `
ALTER TABLE query_executions
    ADD COLUMN trino_catalog TEXT,
    ADD COLUMN result_key  TEXT;
`

const up00010MySQL = `ALTER TABLE query_executions ADD COLUMN IF NOT EXISTS trino_catalog TEXT,
    ADD COLUMN IF NOT EXISTS result_key TEXT;`

const down00010Postgres = `
ALTER TABLE query_executions
    DROP COLUMN IF EXISTS trino_catalog,
    DROP COLUMN IF EXISTS result_key;
`

const down00010MySQL = `ALTER TABLE query_executions DROP COLUMN IF EXISTS trino_catalog,
    DROP COLUMN IF EXISTS result_key;`

func up00010(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, up00010Postgres, up00010MySQL)
}

func down00010(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, down00010Postgres, down00010MySQL)
}
