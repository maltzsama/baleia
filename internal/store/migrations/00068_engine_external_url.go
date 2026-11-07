package migrations

import (
	"context"

	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(up00068, down00068)
}

const up00068Postgres = `
-- The address BI clients reach, which may differ from the in-cluster URL
-- (#533, #560). Nullable: an engine without it cannot be opened from a BI tool.
ALTER TABLE engines ADD COLUMN external_url TEXT;
`

const up00068MySQL = `
ALTER TABLE engines ADD COLUMN external_url TEXT;
`

const down00068Postgres = `
ALTER TABLE engines DROP COLUMN IF EXISTS external_url;
`

const down00068MySQL = `
ALTER TABLE engines DROP COLUMN external_url;
`

func up00068(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, up00068Postgres, up00068MySQL)
}

func down00068(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, down00068Postgres, down00068MySQL)
}
