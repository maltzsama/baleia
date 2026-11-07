package migrations

import (
	"context"

	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(up00072, down00072)
}

const up00072Postgres = `
-- A-5.3 (#600): the per-engine switch that hands admission over to Trino's
-- resource groups. When on, Baleia's admission workflow stops granting slots
-- from its own per-class queue for this engine and lets Trino's resource group
-- hold the run; Baleia keeps only the parts Trino cannot express (fairness and
-- the per-agent limit). A run never waits in both queues.

ALTER TABLE engines
    ADD COLUMN trino_resource_groups_enabled BOOLEAN NOT NULL DEFAULT false;
`

const up00072MySQL = `
ALTER TABLE engines
    ADD COLUMN trino_resource_groups_enabled BOOLEAN NOT NULL DEFAULT false;
`

const down00072Postgres = `
ALTER TABLE engines DROP COLUMN IF EXISTS trino_resource_groups_enabled;
`

const down00072MySQL = `
ALTER TABLE engines DROP COLUMN trino_resource_groups_enabled;
`

func up00072(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, up00072Postgres, up00072MySQL)
}

func down00072(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, down00072Postgres, down00072MySQL)
}
