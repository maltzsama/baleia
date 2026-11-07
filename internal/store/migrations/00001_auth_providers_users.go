package migrations

import (
	"context"

	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(up00001, down00001)
}

const up00001Postgres = `
CREATE TABLE auth_providers (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT        NOT NULL,
    type        TEXT        NOT NULL CHECK (type IN ('oidc')),
    config_enc  TEXT          NOT NULL,
    enabled     BOOLEAN          NOT NULL DEFAULT true,
    created_at  TIMESTAMPTZ   NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ   NOT NULL DEFAULT now()
);

CREATE TABLE users (
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name                  TEXT        NOT NULL,
    email                 TEXT        NOT NULL UNIQUE,
    password_hash         TEXT,
    role                  TEXT        NOT NULL DEFAULT 'member' CHECK (role IN ('admin', 'member')),
    auth_provider         TEXT        NOT NULL DEFAULT 'local' CHECK (auth_provider IN ('local', 'oidc')),
    auth_provider_id      UUID          REFERENCES auth_providers (id) ON DELETE SET NULL,
    external_id           TEXT,
    personal_workspace_id UUID,
    created_at            TIMESTAMPTZ   NOT NULL DEFAULT now(),
    updated_at            TIMESTAMPTZ   NOT NULL DEFAULT now(),
    CONSTRAINT chk_local_auth  CHECK (auth_provider != 'local'  OR password_hash IS NOT NULL),
    CONSTRAINT chk_oidc_auth   CHECK (auth_provider != 'oidc'   OR (auth_provider_id IS NOT NULL AND external_id IS NOT NULL)),
    UNIQUE (auth_provider_id, external_id)
);

CREATE TABLE api_keys (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID          NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name        TEXT        NOT NULL,
    key_hash    TEXT        NOT NULL UNIQUE,
    expires_at  TIMESTAMPTZ,
    created_at  TIMESTAMPTZ   NOT NULL DEFAULT now()
);

CREATE INDEX idx_users_email          ON users (email);
CREATE INDEX idx_users_external_id    ON users (auth_provider_id, external_id);
CREATE INDEX idx_api_keys_user_id     ON api_keys (user_id);
CREATE INDEX idx_api_keys_key_hash    ON api_keys (key_hash);
`

const up00001MySQL = `CREATE TABLE auth_providers (id          CHAR(36)     NOT NULL DEFAULT (UUID()) PRIMARY KEY,
    name        VARCHAR(255) NOT NULL,
    type        VARCHAR(32)  NOT NULL,
    config_enc  TEXT         NOT NULL,
    enabled     BOOLEAN      NOT NULL DEFAULT TRUE,
    created_at  DATETIME(6)  NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at  DATETIME(6)  NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    CONSTRAINT chk_auth_providers_type CHECK (type IN ('oidc'))) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
CREATE TABLE users (id                    CHAR(36)     NOT NULL DEFAULT (UUID()) PRIMARY KEY,
    name                  VARCHAR(255) NOT NULL,
    email                 VARCHAR(320) NOT NULL,
    password_hash         TEXT,
    role                  VARCHAR(32)  NOT NULL DEFAULT ('member'),
    auth_provider         VARCHAR(32)  NOT NULL DEFAULT ('local'),
    auth_provider_id      CHAR(36),
    external_id           VARCHAR(255),
    personal_workspace_id CHAR(36),
    created_at            DATETIME(6)  NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at            DATETIME(6)  NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    CONSTRAINT uq_users_email UNIQUE (email),
    CONSTRAINT chk_users_role CHECK (role IN ('admin', 'member')),
    CONSTRAINT chk_users_auth_provider CHECK (auth_provider IN ('local', 'oidc')),
    CONSTRAINT chk_local_auth  CHECK (auth_provider <> 'local' OR password_hash IS NOT NULL),
    CONSTRAINT chk_oidc_auth   CHECK (auth_provider <> 'oidc'  OR (auth_provider_id IS NOT NULL AND external_id IS NOT NULL)),
    CONSTRAINT uq_users_auth_provider_external UNIQUE (auth_provider_id, external_id),
    CONSTRAINT fk_users_auth_provider FOREIGN KEY (auth_provider_id)
        REFERENCES auth_providers (id) ON DELETE RESTRICT) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
CREATE TABLE api_keys (id          CHAR(36)     NOT NULL DEFAULT (UUID()) PRIMARY KEY,
    user_id     CHAR(36)     NOT NULL,
    name        VARCHAR(255) NOT NULL,
    key_hash    VARCHAR(255) NOT NULL,
    expires_at  DATETIME(6),
    created_at  DATETIME(6)  NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    CONSTRAINT uq_api_keys_key_hash UNIQUE (key_hash(255)),
    CONSTRAINT fk_api_keys_user FOREIGN KEY (user_id)
        REFERENCES users (id) ON DELETE CASCADE) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
CREATE INDEX IF NOT EXISTS idx_users_email       ON users (email(255));
CREATE INDEX IF NOT EXISTS idx_users_external_id ON users (auth_provider_id, external_id(255));
CREATE INDEX IF NOT EXISTS idx_api_keys_user_id  ON api_keys (user_id);
CREATE INDEX IF NOT EXISTS idx_api_keys_key_hash ON api_keys (key_hash(255));`

func up00001(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, up00001Postgres, up00001MySQL)
}

func down00001(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, `
DROP TABLE IF EXISTS api_keys;
DROP TABLE IF EXISTS users;
DROP TABLE IF EXISTS auth_providers;
`, `
DROP TABLE IF EXISTS api_keys;
DROP TABLE IF EXISTS users;
DROP TABLE IF EXISTS auth_providers;
`)
}
