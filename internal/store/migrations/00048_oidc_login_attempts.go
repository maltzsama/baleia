package migrations

import (
	"context"

	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(up00048, down00048)
}

const up00048Postgres = `
-- Server-side OIDC login attempts (#250). The state is the primary key and the
-- row is consumed atomically, so a replayed callback finds nothing. The PKCE
-- verifier never leaves the server; nonce is reserved for the nonce slice.
CREATE TABLE oidc_login_attempts (
    state         TEXT PRIMARY KEY,
    provider_id   UUID NOT NULL REFERENCES auth_providers(id) ON DELETE CASCADE,
    nonce         TEXT,
    code_verifier TEXT NOT NULL,
    redirect      TEXT,
    expires_at    TIMESTAMPTZ NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_oidc_login_attempts_expires_at ON oidc_login_attempts(expires_at);
`

const up00048MySQL = `CREATE TABLE oidc_login_attempts (state         VARCHAR(255) PRIMARY KEY,
    provider_id   CHAR(36) NOT NULL,
    nonce         TEXT,
    code_verifier TEXT NOT NULL,
    redirect      TEXT,
    expires_at    DATETIME(6) NOT NULL,
    created_at    DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    CONSTRAINT oidc_login_attempts_provider_id_fkey FOREIGN KEY (provider_id) REFERENCES auth_providers(id) ON DELETE CASCADE) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
CREATE INDEX IF NOT EXISTS idx_oidc_login_attempts_expires_at ON oidc_login_attempts(expires_at);`

const down00048Postgres = `
DROP TABLE IF EXISTS oidc_login_attempts;
`

const down00048MySQL = `DROP TABLE IF EXISTS oidc_login_attempts;`

func up00048(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, up00048Postgres, up00048MySQL)
}

func down00048(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, down00048Postgres, down00048MySQL)
}
