package migrations

import (
	"context"

	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(up00066, down00066)
}

const up00066Postgres = `
-- Optional cost routing for Trino (#550). When enabled, classification runs
-- EXPLAIN (FORMAT JSON) for a SELECT and moves a run whose estimate exceeds
-- the threshold into the heavy class.
ALTER TABLE engines
    ADD COLUMN cost_routing_enabled BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN cost_threshold_bytes BIGINT
        CHECK (cost_threshold_bytes IS NULL OR cost_threshold_bytes > 0);
`


const up00066MySQL = `
ALTER TABLE engines
    ADD COLUMN cost_routing_enabled BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN cost_threshold_bytes BIGINT
        CHECK (cost_threshold_bytes IS NULL OR cost_threshold_bytes > 0);
`


const down00066Postgres = `
ALTER TABLE engines
    DROP COLUMN IF EXISTS cost_threshold_bytes,
    DROP COLUMN IF EXISTS cost_routing_enabled;
`


const down00066MySQL = `
ALTER TABLE engines
    DROP COLUMN cost_threshold_bytes,
    DROP COLUMN cost_routing_enabled;
`


func up00066(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, up00066Postgres, up00066MySQL)
}

func down00066(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, down00066Postgres, down00066MySQL)
}
