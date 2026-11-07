package migrations

import (
	"context"

	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(up00025, down00025)
}

const up00025Postgres = `
-- Native catalogs (found inside an engine, #13) have no Trino projection, so
-- the Trino columns of catalogs become nullable. Source catalogs keep them:
-- the CHECK holds what NOT NULL held for every row before.
ALTER TABLE catalogs
    ALTER COLUMN trino_catalog_name DROP NOT NULL,
    ALTER COLUMN cluster_id DROP NOT NULL,
    ALTER COLUMN warehouse DROP NOT NULL,
    ALTER COLUMN connector DROP NOT NULL,
    ADD CONSTRAINT catalogs_source_has_trino_columns CHECK (
        origin = 'native'
        OR (trino_catalog_name IS NOT NULL AND cluster_id IS NOT NULL
            AND warehouse IS NOT NULL AND connector IS NOT NULL));
`

const up00025MySQL = `ALTER TABLE catalogs MODIFY COLUMN trino_catalog_name TEXT NULL,
    MODIFY COLUMN cluster_id CHAR(36) NULL,
    MODIFY COLUMN warehouse TEXT NULL,
    MODIFY COLUMN connector TEXT NULL,
    ADD CONSTRAINT catalogs_source_has_trino_columns CHECK (
        origin = 'native'
        OR (trino_catalog_name IS NOT NULL AND cluster_id IS NOT NULL
            AND warehouse IS NOT NULL AND connector IS NOT NULL));`

const down00025Postgres = `
-- Native catalogs cannot exist without the Trino columns; their bindings go
-- with them (ON DELETE CASCADE).
DELETE FROM catalogs WHERE origin = 'native';

ALTER TABLE catalogs
    DROP CONSTRAINT IF EXISTS catalogs_source_has_trino_columns,
    ALTER COLUMN trino_catalog_name SET NOT NULL,
    ALTER COLUMN cluster_id SET NOT NULL,
    ALTER COLUMN warehouse SET NOT NULL,
    ALTER COLUMN connector SET NOT NULL;
`

const down00025MySQL = `ALTER TABLE catalogs DROP CONSTRAINT IF EXISTS catalogs_source_has_trino_columns,
    MODIFY COLUMN trino_catalog_name TEXT NOT NULL,
    MODIFY COLUMN cluster_id CHAR(36) NOT NULL,
    MODIFY COLUMN warehouse TEXT NOT NULL,
    MODIFY COLUMN connector TEXT NOT NULL;`

func up00025(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, up00025Postgres, up00025MySQL)
}

func down00025(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, down00025Postgres, down00025MySQL)
}
