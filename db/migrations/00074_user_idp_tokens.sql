-- +goose Up
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

-- +goose Down
DROP TABLE user_idp_tokens;
