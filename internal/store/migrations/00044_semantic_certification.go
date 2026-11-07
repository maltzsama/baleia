package migrations

import (
	"context"

	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(up00044, down00044)
}

const up00044Postgres = `
ALTER TABLE semantic_metrics
    ADD COLUMN certified_by UUID REFERENCES users (id) ON DELETE SET NULL,
    ADD COLUMN certified_at TIMESTAMPTZ;

UPDATE semantic_metrics SET certified_at = updated_at WHERE certified;
`

const up00044MySQL = `
ALTER TABLE semantic_metrics
    ADD COLUMN certified_by CHAR(36) REFERENCES users (id) ON DELETE SET NULL,
    ADD COLUMN certified_at DATETIME(6);
`

const down00044Postgres = `
ALTER TABLE semantic_metrics
    DROP COLUMN IF EXISTS certified_at,
    DROP COLUMN IF EXISTS certified_by;
`

const down00044MySQL = `
ALTER TABLE semantic_metrics
    DROP COLUMN certified_at,
    DROP COLUMN certified_by;
`

func up00044(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, up00044Postgres, up00044MySQL)
}

func down00044(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, down00044Postgres, down00044MySQL)
}
