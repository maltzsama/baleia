package migrations

import (
	"context"

	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(up00013, down00013)
}

const up00013Postgres = `
ALTER TABLE catalog_connections
    DROP CONSTRAINT catalog_connections_type_check;

ALTER TABLE catalog_connections
    ADD CONSTRAINT catalog_connections_type_check
    CHECK (type IN ('polaris', 'nessie', 'glue', 'hive', 'iceberg', 'postgresql', 'mysql'));
`

const up00013MySQL = `ALTER TABLE catalog_connections DROP CONSTRAINT IF EXISTS catalog_connections_type_check;
ALTER TABLE catalog_connections ADD CONSTRAINT catalog_connections_type_check
    CHECK (type IN ('polaris', 'nessie', 'glue', 'hive', 'iceberg', 'postgresql', 'mysql'));`

const down00013Postgres = `
ALTER TABLE catalog_connections
    DROP CONSTRAINT catalog_connections_type_check;

ALTER TABLE catalog_connections
    ADD CONSTRAINT catalog_connections_type_check
    CHECK (type IN ('polaris', 'nessie', 'glue', 'hive'));
`

const down00013MySQL = `ALTER TABLE catalog_connections DROP CONSTRAINT IF EXISTS catalog_connections_type_check;
ALTER TABLE catalog_connections ADD CONSTRAINT catalog_connections_type_check
    CHECK (type IN ('polaris', 'nessie', 'glue', 'hive'));`

func up00013(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, up00013Postgres, up00013MySQL)
}

func down00013(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, down00013Postgres, down00013MySQL)
}
