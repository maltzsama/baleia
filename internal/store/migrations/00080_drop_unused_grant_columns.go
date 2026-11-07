package migrations

import (
	"context"

	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(up00080, down00080)
}

const up00080Postgres = `
-- #654: the grant's read/write permission and the never-enforced
-- default_readonly flag are no longer read or written (#651). They stayed
-- through 0.6.1 and 0.6.2 so a pod from the previous release could finish its
-- rolling update while still naming them; now they are dropped.
ALTER TABLE workspace_catalog_grants DROP COLUMN permission;
ALTER TABLE catalog_connections DROP COLUMN default_readonly;
`

const up00080MySQL = `ALTER TABLE workspace_catalog_grants DROP COLUMN IF EXISTS permission;
ALTER TABLE catalog_connections DROP COLUMN IF EXISTS default_readonly;`

const down00080Postgres = `
ALTER TABLE workspace_catalog_grants
    ADD COLUMN permission TEXT NOT NULL DEFAULT 'write' CHECK (permission IN ('read', 'write'));
ALTER TABLE catalog_connections
    ADD COLUMN default_readonly BOOLEAN NOT NULL DEFAULT true;
`

const down00080MySQL = `ALTER TABLE workspace_catalog_grants ADD COLUMN IF NOT EXISTS permission TEXT NOT NULL DEFAULT ('write'),
    ADD CONSTRAINT workspace_catalog_grants_permission_check CHECK (permission IN ('read', 'write'));
ALTER TABLE catalog_connections ADD COLUMN IF NOT EXISTS default_readonly BOOLEAN NOT NULL DEFAULT true;`

func up00080(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, up00080Postgres, up00080MySQL)
}

func down00080(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, down00080Postgres, down00080MySQL)
}
