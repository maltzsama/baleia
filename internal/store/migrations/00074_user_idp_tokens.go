package migrations

import (
	"context"

	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(up00074, down00074)
}

const up00074Postgres = `
-- #648: the identity-provider tokens of a user's latest OIDC sign-in. Baleia
-- sends the access token through Trino to catalog managers set to per-user
-- identity, which authorize that user themselves, and refreshes it with the
-- refresh token while it is valid. Both are sealed with the keyring (bound to
-- the user id) and never leave the server otherwise. A sign-out, a disabled
-- account or a removed provider deletes the row.
CREATE TABLE user_idp_tokens (
    user_id           UUID        PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    auth_provider_id  UUID        NOT NULL REFERENCES auth_providers (id) ON DELETE CASCADE,
    access_token_enc  TEXT        NOT NULL,
    refresh_token_enc TEXT,
    expires_at        TIMESTAMPTZ NOT NULL,
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);
`


const up00074MySQL = `
CREATE TABLE user_idp_tokens (
    user_id           CHAR(36)        PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    auth_provider_id  CHAR(36)        NOT NULL REFERENCES auth_providers (id) ON DELETE CASCADE,
    access_token_enc  TEXT        NOT NULL,
    refresh_token_enc TEXT,
    expires_at        DATETIME(6) NOT NULL,
    updated_at        DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
`


const down00074Postgres = `
DROP TABLE user_idp_tokens;
`


const down00074MySQL = `
DROP TABLE user_idp_tokens;
`


func up00074(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, up00074Postgres, up00074MySQL)
}

func down00074(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, down00074Postgres, down00074MySQL)
}
