-- +goose Up

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

-- +goose Down

DROP TABLE IF EXISTS oidc_login_attempts;
