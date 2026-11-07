package migrations

import (
	"context"

	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(up00009, down00009)
}

const up00009Postgres = `
CREATE TABLE refresh_tokens (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash TEXT NOT NULL UNIQUE,
    expires_at TIMESTAMPTZ NOT NULL,
    revoked    BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_refresh_tokens_user_id ON refresh_tokens(user_id);
CREATE INDEX idx_refresh_tokens_expires_at ON refresh_tokens(expires_at);
`

const up00009MySQL = `CREATE TABLE refresh_tokens (id         CHAR(36) PRIMARY KEY DEFAULT (UUID()),
    user_id    CHAR(36) NOT NULL,
    token_hash TEXT NOT NULL,
    expires_at DATETIME(6) NOT NULL,
    revoked    BOOLEAN NOT NULL DEFAULT false,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    CONSTRAINT refresh_tokens_user_id_fkey FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
    CONSTRAINT refresh_tokens_token_hash_key UNIQUE (token_hash(255))) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
CREATE INDEX IF NOT EXISTS idx_refresh_tokens_user_id ON refresh_tokens(user_id);
CREATE INDEX IF NOT EXISTS idx_refresh_tokens_expires_at ON refresh_tokens(expires_at);`

const down00009Postgres = `
DROP TABLE IF EXISTS refresh_tokens;
`

const down00009MySQL = `DROP TABLE IF EXISTS refresh_tokens;`

func up00009(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, up00009Postgres, up00009MySQL)
}

func down00009(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, down00009Postgres, down00009MySQL)
}
