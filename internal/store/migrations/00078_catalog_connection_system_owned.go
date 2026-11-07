package migrations

import (
	"context"

	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(up00078, down00078)
}

const up00078Postgres = `
-- #694: an immutable owner marker for the platform's own connections (the
-- default Trino cluster, the operator's catalog managers). created_by IS NULL
-- alone cannot tell a system-owned connection from one whose owner was deleted
-- (00015 uses ON DELETE SET NULL), and only the former may reach a private
-- address through the managed dialer (#609).
ALTER TABLE catalog_connections ADD COLUMN system_owned BOOLEAN NOT NULL DEFAULT false;

-- Existing rows keep today's behavior: every created_by IS NULL row was
-- treated as system-owned before the marker. New orphans (created_by nulled by
-- a user deletion) keep false and stay on the strict dialer.
UPDATE catalog_connections SET system_owned = true WHERE created_by IS NULL;
`

const up00078MySQL = `ALTER TABLE catalog_connections ADD COLUMN IF NOT EXISTS system_owned BOOLEAN NOT NULL DEFAULT false;`

const down00078Postgres = `
ALTER TABLE catalog_connections DROP COLUMN system_owned;
`

const down00078MySQL = `ALTER TABLE catalog_connections DROP COLUMN IF EXISTS system_owned;`

func up00078(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, up00078Postgres, up00078MySQL)
}

func down00078(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, down00078Postgres, down00078MySQL)
}
