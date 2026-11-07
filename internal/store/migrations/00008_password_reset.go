package migrations

import (
	"context"

	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(up00008, down00008)
}

const up00008Postgres = `
ALTER TABLE users
    ADD COLUMN password_reset_token      TEXT,
    ADD COLUMN password_reset_expires_at TIMESTAMPTZ;
`

const up00008MySQL = `
ALTER TABLE users
    ADD COLUMN password_reset_token      TEXT,
    ADD COLUMN password_reset_expires_at DATETIME(6);
`

const down00008Postgres = `
ALTER TABLE users
    DROP COLUMN IF EXISTS password_reset_token,
    DROP COLUMN IF EXISTS password_reset_expires_at;
`

const down00008MySQL = `
ALTER TABLE users
    DROP COLUMN password_reset_token,
    DROP COLUMN password_reset_expires_at;
`

func up00008(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, up00008Postgres, up00008MySQL)
}

func down00008(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, down00008Postgres, down00008MySQL)
}
