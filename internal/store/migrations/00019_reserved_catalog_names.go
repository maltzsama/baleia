package migrations

import (
	"context"

	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(up00019, down00019)
}

const up00019Postgres = `
-- The baleia-trino-catalog-store plugin (v0.3.0+) reserves "env" and "file" as
-- catalog names because they collide with the @baleia-secret[<origin>:<key>]
-- origin schemes. Extend the CHECK to match the plugin's CatalogRow validation.
ALTER TABLE catalogs
    DROP CONSTRAINT catalogs_trino_name_not_reserved;

ALTER TABLE catalogs
    ADD CONSTRAINT catalogs_trino_name_not_reserved
    CHECK (trino_catalog_name NOT IN ('system', 'jmx', 'tpch', 'tpcds', 'memory', 'env', 'file'));
`

const up00019MySQL = `ALTER TABLE catalogs DROP CONSTRAINT IF EXISTS catalogs_trino_name_not_reserved;
ALTER TABLE catalogs ADD CONSTRAINT catalogs_trino_name_not_reserved
    CHECK (trino_catalog_name NOT IN ('system', 'jmx', 'tpch', 'tpcds', 'memory', 'env', 'file'));`

const down00019Postgres = `
ALTER TABLE catalogs
    DROP CONSTRAINT catalogs_trino_name_not_reserved;

ALTER TABLE catalogs
    ADD CONSTRAINT catalogs_trino_name_not_reserved
    CHECK (trino_catalog_name NOT IN ('system', 'jmx', 'tpch', 'tpcds', 'memory'));
`

const down00019MySQL = `ALTER TABLE catalogs DROP CONSTRAINT IF EXISTS catalogs_trino_name_not_reserved;
ALTER TABLE catalogs ADD CONSTRAINT catalogs_trino_name_not_reserved
    CHECK (trino_catalog_name NOT IN ('system', 'jmx', 'tpch', 'tpcds', 'memory'));`

func up00019(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, up00019Postgres, up00019MySQL)
}

func down00019(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, down00019Postgres, down00019MySQL)
}
