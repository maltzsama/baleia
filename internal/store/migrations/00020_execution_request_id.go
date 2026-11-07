package migrations

import (
	"context"

	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(up00020, down00020)
}

const up00020Postgres = `
-- Correlation: keep the HTTP X-Request-ID on the execution row so a query can
-- be traced from the request log through the Temporal workflow and back.
ALTER TABLE query_executions ADD COLUMN request_id TEXT;
`


const up00020MySQL = `
ALTER TABLE query_executions ADD COLUMN request_id TEXT;
`


const down00020Postgres = `
ALTER TABLE query_executions DROP COLUMN request_id;
`


const down00020MySQL = `
ALTER TABLE query_executions DROP COLUMN request_id;
`


func up00020(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, up00020Postgres, up00020MySQL)
}

func down00020(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, down00020Postgres, down00020MySQL)
}
