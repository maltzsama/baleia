-- +goose Up

ALTER TABLE users
    ADD COLUMN password_reset_token      TEXT,
    ADD COLUMN password_reset_expires_at TIMESTAMPTZ;

-- +goose Down

ALTER TABLE users
    DROP COLUMN IF EXISTS password_reset_token,
    DROP COLUMN IF EXISTS password_reset_expires_at;