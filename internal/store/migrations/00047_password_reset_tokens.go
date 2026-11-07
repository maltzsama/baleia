package migrations

import (
	"context"

	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(up00047, down00047)
}

const up00047Postgres = `
-- Password-reset tokens are stored as SHA-256 hashes and are single use (#51).
-- A row is consumed by setting used_at; expired and used rows are pruned by the
-- daily token-cleanup schedule.
CREATE TABLE password_reset_tokens (
    token_hash TEXT PRIMARY KEY,
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at TIMESTAMPTZ NOT NULL,
    used_at    TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_password_reset_tokens_user_id ON password_reset_tokens(user_id);
CREATE INDEX idx_password_reset_tokens_expires_at ON password_reset_tokens(expires_at);

-- Outstanding links are invalidated on deploy: the plaintext columns are
-- removed rather than migrated.
ALTER TABLE users
    DROP COLUMN IF EXISTS password_reset_token,
    DROP COLUMN IF EXISTS password_reset_expires_at;
`


const up00047MySQL = `
CREATE TABLE password_reset_tokens (
    token_hash TEXT PRIMARY KEY,
    user_id    CHAR(36) NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at DATETIME(6) NOT NULL,
    used_at    DATETIME(6),
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE INDEX idx_password_reset_tokens_user_id ON password_reset_tokens(user_id);
CREATE INDEX idx_password_reset_tokens_expires_at ON password_reset_tokens(expires_at);

ALTER TABLE users
    DROP COLUMN password_reset_token,
    DROP COLUMN password_reset_expires_at;
`


const down00047Postgres = `
ALTER TABLE users
    ADD COLUMN password_reset_token TEXT,
    ADD COLUMN password_reset_expires_at TIMESTAMPTZ;

DROP TABLE IF EXISTS password_reset_tokens;
`


const down00047MySQL = `
ALTER TABLE users
    ADD COLUMN password_reset_token TEXT,
    ADD COLUMN password_reset_expires_at DATETIME(6);

DROP TABLE IF EXISTS password_reset_tokens;
`


func up00047(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, up00047Postgres, up00047MySQL)
}

func down00047(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, down00047Postgres, down00047MySQL)
}
