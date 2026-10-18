-- +goose Up

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

-- +goose Down

ALTER TABLE users
    ADD COLUMN password_reset_token TEXT,
    ADD COLUMN password_reset_expires_at TIMESTAMPTZ;

DROP TABLE IF EXISTS password_reset_tokens;
