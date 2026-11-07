package migrations

import (
	"context"

	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(up00015, down00015)
}

const up00015Postgres = `
ALTER TABLE catalog_connections ADD COLUMN created_by UUID REFERENCES users(id) ON DELETE SET NULL;
`

const up00015MySQL = `ALTER TABLE catalog_connections ADD COLUMN IF NOT EXISTS created_by CHAR(36),
    ADD CONSTRAINT catalog_connections_created_by_fkey FOREIGN KEY (created_by) REFERENCES users(id) ON DELETE SET NULL;`

const down00015Postgres = `
ALTER TABLE catalog_connections DROP COLUMN created_by;
`

const down00015MySQL = `ALTER TABLE catalog_connections DROP COLUMN IF EXISTS created_by;`

func up00015(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, up00015Postgres, up00015MySQL)
}

func down00015(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, down00015Postgres, down00015MySQL)
}
