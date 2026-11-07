package migrations

import (
	"context"

	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(up00049, down00049)
}

const up00049Postgres = `
-- Defence in depth for the stored post-login redirect (#252): the handler
-- validates it too, but the database refuses scheme-relative, backslash and
-- control-character escapes even if a future writer forgets.
ALTER TABLE oidc_login_attempts
    ADD CONSTRAINT oidc_login_attempts_redirect_check
    CHECK (
        redirect IS NULL
        OR (
            redirect LIKE '/%'
            AND redirect NOT LIKE '//%'
            AND redirect !~ '\\'
            AND redirect !~ '[\r\n\t]'
        )
    );
`

const up00049MySQL = `ALTER TABLE oidc_login_attempts ADD CONSTRAINT oidc_login_attempts_redirect_check
    CHECK (
        redirect IS NULL
        OR (
            redirect LIKE '/%'
            AND redirect NOT LIKE '//%'
            AND redirect NOT REGEXP '\\\\'
            AND redirect NOT REGEXP '[\r\n\t]'
        )
    );`

const down00049Postgres = `
ALTER TABLE oidc_login_attempts
    DROP CONSTRAINT IF EXISTS oidc_login_attempts_redirect_check;
`

const down00049MySQL = `ALTER TABLE oidc_login_attempts DROP CONSTRAINT IF EXISTS oidc_login_attempts_redirect_check;`

func up00049(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, up00049Postgres, up00049MySQL)
}

func down00049(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, down00049Postgres, down00049MySQL)
}
