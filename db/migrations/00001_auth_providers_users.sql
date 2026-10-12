-- +goose Up

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

-- +goose Down

DROP TABLE IF EXISTS api_keys;
DROP TABLE IF EXISTS users;
DROP TABLE IF EXISTS auth_providers;