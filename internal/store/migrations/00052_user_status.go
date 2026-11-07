package migrations

import (
	"context"

	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(up00052, down00052)
}

const up00052Postgres = `
-- Suspend, never delete (#256). status is the only disable switch; the
-- suspended_* columns explain when and why.
ALTER TABLE users
    ADD COLUMN status              TEXT        NOT NULL DEFAULT 'active',
    ADD COLUMN suspended_at        TIMESTAMPTZ,
    ADD COLUMN suspended_reason    TEXT,
    ADD COLUMN directory_synced_at TIMESTAMPTZ;

ALTER TABLE users ADD CONSTRAINT users_status_check
    CHECK (status IN ('active', 'suspended'));
`

const up00052MySQL = `ALTER TABLE users ADD COLUMN IF NOT EXISTS status TEXT        NOT NULL DEFAULT ('active'),
    ADD COLUMN IF NOT EXISTS suspended_at DATETIME(6),
    ADD COLUMN IF NOT EXISTS suspended_reason TEXT,
    ADD COLUMN IF NOT EXISTS directory_synced_at DATETIME(6);
ALTER TABLE users ADD CONSTRAINT users_status_check
    CHECK (status IN ('active', 'suspended'));`

const down00052Postgres = `
ALTER TABLE users DROP CONSTRAINT IF EXISTS users_status_check;

ALTER TABLE users
    DROP COLUMN IF EXISTS directory_synced_at,
    DROP COLUMN IF EXISTS suspended_reason,
    DROP COLUMN IF EXISTS suspended_at,
    DROP COLUMN IF EXISTS status;
`

const down00052MySQL = `ALTER TABLE users DROP CONSTRAINT IF EXISTS users_status_check;
ALTER TABLE users DROP COLUMN IF EXISTS directory_synced_at,
    DROP COLUMN IF EXISTS suspended_reason,
    DROP COLUMN IF EXISTS suspended_at,
    DROP COLUMN IF EXISTS status;`

func up00052(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, up00052Postgres, up00052MySQL)
}

func down00052(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, down00052Postgres, down00052MySQL)
}
