package migrations

import (
	"context"

	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(up00014, down00014)
}

const up00014Postgres = `
ALTER TABLE catalog_connections
    ADD COLUMN host TEXT,
    ADD COLUMN port TEXT;

ALTER TABLE catalogs
    DROP COLUMN trino_catalog_name;
`


const up00014MySQL = `
ALTER TABLE catalog_connections
    ADD COLUMN host TEXT,
    ADD COLUMN port TEXT;

ALTER TABLE catalogs
    DROP COLUMN trino_catalog_name;
`


const down00014Postgres = `
ALTER TABLE catalog_connections
    DROP COLUMN IF EXISTS host,
    DROP COLUMN IF EXISTS port;

ALTER TABLE catalogs
    ADD COLUMN trino_catalog_name TEXT NOT NULL DEFAULT '';
`


const down00014MySQL = `
ALTER TABLE catalog_connections
    DROP COLUMN host,
    DROP COLUMN port;

ALTER TABLE catalogs
    ADD COLUMN trino_catalog_name TEXT NOT NULL DEFAULT '';
`


func up00014(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, up00014Postgres, up00014MySQL)
}

func down00014(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, down00014Postgres, down00014MySQL)
}
