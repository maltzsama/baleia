package migrations

import (
	"context"

	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(up00058, down00058)
}

const up00058Postgres = `
-- UI language preference (#67). NULL means "follow the browser"; the locale
-- selection lives with the user so it follows them across devices.
ALTER TABLE users
    ADD COLUMN locale TEXT;

ALTER TABLE users ADD CONSTRAINT users_locale_check
    CHECK (locale IN ('en', 'pt-BR'));
`

const up00058MySQL = `
ALTER TABLE users
    ADD COLUMN locale TEXT;

ALTER TABLE users ADD CONSTRAINT users_locale_check
    CHECK (locale IN ('en', 'pt-BR'));
`

const down00058Postgres = `
ALTER TABLE users DROP CONSTRAINT IF EXISTS users_locale_check;

ALTER TABLE users DROP COLUMN IF EXISTS locale;
`

const down00058MySQL = `
ALTER TABLE users DROP CONSTRAINT users_locale_check;

ALTER TABLE users DROP COLUMN locale;
`

func up00058(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, up00058Postgres, up00058MySQL)
}

func down00058(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, down00058Postgres, down00058MySQL)
}
