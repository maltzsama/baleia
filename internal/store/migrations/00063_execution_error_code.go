package migrations

import (
	"context"

	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(up00063, down00063)
}

const up00063Postgres = `
-- A run can fail before any statement runs — e.g. the admission queue timed
-- out or was full (#399). It carries an error code with no statement to hold
-- it, so the execution itself records it.
ALTER TABLE query_executions ADD COLUMN error_code TEXT;
`

const up00063MySQL = `ALTER TABLE query_executions ADD COLUMN IF NOT EXISTS error_code TEXT;`

const down00063Postgres = `
ALTER TABLE query_executions DROP COLUMN IF EXISTS error_code;
`

const down00063MySQL = `ALTER TABLE query_executions DROP COLUMN IF EXISTS error_code;`

func up00063(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, up00063Postgres, up00063MySQL)
}

func down00063(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, down00063Postgres, down00063MySQL)
}
