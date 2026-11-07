package migrations

import (
	"context"

	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(up00023, down00023)
}

const up00023Postgres = `
-- Every kind the provider registry may offer (#11). The API accepts only the
-- kinds that have a registered provider; the CHECK already allows the rest.
ALTER TABLE catalog_connections
    DROP CONSTRAINT catalog_connections_type_check;

ALTER TABLE catalog_connections
    ADD CONSTRAINT catalog_connections_type_check
    CHECK (type IN (
        'polaris', 'nessie', 'iceberg', 'lakekeeper', 'unity', 'hive', 'glue',
        'postgresql', 'mysql', 'mariadb', 'sqlserver', 'oracle', 'sqlite', 'clickhouse',
        'flightsql', 'trino', 'doris', 'starrocks'));

-- config holds the non-secret values of the provider's ConfigSchema; secrets
-- stay sealed in credentials_enc. credentials_read_only is an admin assertion
-- that the stored credentials cannot write (docs/adr/0011, #20).
ALTER TABLE catalog_connections
    ADD COLUMN config JSONB NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN credentials_read_only BOOLEAN NOT NULL DEFAULT false;

-- Backfill from the columns config replaces. The application converts what
-- remains in the url (a JDBC URL for databases) and in the credentials blob.
-- url/host/port and default_readonly are kept for one release and dropped in
-- a follow-up.
UPDATE catalog_connections
SET config = jsonb_strip_nulls(jsonb_build_object('url', url, 'host', host, 'port', port));
`

const up00023MySQL = `
ALTER TABLE catalog_connections
    DROP CONSTRAINT catalog_connections_type_check;

ALTER TABLE catalog_connections
    ADD CONSTRAINT catalog_connections_type_check
    CHECK (type IN (
        'polaris', 'nessie', 'iceberg', 'lakekeeper', 'unity', 'hive', 'glue',
        'postgresql', 'mysql', 'mariadb', 'sqlserver', 'oracle', 'sqlite', 'clickhouse',
        'flightsql', 'trino', 'doris', 'starrocks'));

ALTER TABLE catalog_connections
    ADD COLUMN config JSON NOT NULL DEFAULT '{}',
    ADD COLUMN credentials_read_only BOOLEAN NOT NULL DEFAULT false;
`

const down00023Postgres = `
ALTER TABLE catalog_connections
    DROP COLUMN IF EXISTS credentials_read_only,
    DROP COLUMN IF EXISTS config;

ALTER TABLE catalog_connections
    DROP CONSTRAINT catalog_connections_type_check;

ALTER TABLE catalog_connections
    ADD CONSTRAINT catalog_connections_type_check
    CHECK (type IN ('polaris', 'nessie', 'glue', 'hive', 'iceberg', 'postgresql', 'mysql'));
`

const down00023MySQL = `
ALTER TABLE catalog_connections
    DROP COLUMN credentials_read_only,
    DROP COLUMN config;

ALTER TABLE catalog_connections
    DROP CONSTRAINT catalog_connections_type_check;

ALTER TABLE catalog_connections
    ADD CONSTRAINT catalog_connections_type_check
    CHECK (type IN ('polaris', 'nessie', 'glue', 'hive', 'iceberg', 'postgresql', 'mysql'));
`

func up00023(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, up00023Postgres, up00023MySQL)
}

func down00023(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, down00023Postgres, down00023MySQL)
}
