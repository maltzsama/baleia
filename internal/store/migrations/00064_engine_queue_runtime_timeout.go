package migrations

import (
	"context"

	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(up00064, down00064)
}

const up00064Postgres = `
-- Runtime bound per workload class (#576): a run that waited up to 24h in the
-- queue must not be killed by the 30m global statement timeout. NULL keeps the
-- class default resolved by the API.
ALTER TABLE engine_queue_capacity
    ADD COLUMN runtime_timeout_ms BIGINT
    CHECK (runtime_timeout_ms IS NULL OR runtime_timeout_ms > 0);
`

const up00064MySQL = `
ALTER TABLE engine_queue_capacity
    ADD COLUMN runtime_timeout_ms BIGINT
    CHECK (runtime_timeout_ms IS NULL OR runtime_timeout_ms > 0);
`

const down00064Postgres = `
ALTER TABLE engine_queue_capacity DROP COLUMN IF EXISTS runtime_timeout_ms;
`

const down00064MySQL = `
ALTER TABLE engine_queue_capacity DROP COLUMN runtime_timeout_ms;
`

func up00064(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, up00064Postgres, up00064MySQL)
}

func down00064(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, down00064Postgres, down00064MySQL)
}
