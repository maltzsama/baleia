package migrations

import (
	"context"

	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(up00021, down00021)
}

const up00021Postgres = `
ALTER TABLE query_statements
    ADD COLUMN result_key   TEXT,
    ADD COLUMN result_bytes BIGINT,
    ADD COLUMN truncated    BOOLEAN NOT NULL DEFAULT false;
`

const up00021MySQL = `ALTER TABLE query_statements ADD COLUMN IF NOT EXISTS result_key TEXT,
    ADD COLUMN IF NOT EXISTS result_bytes BIGINT,
    ADD COLUMN IF NOT EXISTS truncated BOOLEAN NOT NULL DEFAULT false;`

const down00021Postgres = `
ALTER TABLE query_statements
    DROP COLUMN IF EXISTS truncated,
    DROP COLUMN IF EXISTS result_bytes,
    DROP COLUMN IF EXISTS result_key;
`

const down00021MySQL = `ALTER TABLE query_statements DROP COLUMN IF EXISTS truncated,
    DROP COLUMN IF EXISTS result_bytes,
    DROP COLUMN IF EXISTS result_key;`

func up00021(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, up00021Postgres, up00021MySQL)
}

func down00021(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, down00021Postgres, down00021MySQL)
}
