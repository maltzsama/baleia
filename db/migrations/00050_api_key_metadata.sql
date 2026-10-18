-- +goose Up

-- API-key metadata for the X-Api-Key middleware (#276). The secret is still
-- stored only as a hash; prefix is the displayable part of the raw key, scopes
-- are '*' today, and last_used_at is touched on successful authentication.
ALTER TABLE api_keys
    ADD COLUMN prefix       TEXT        NOT NULL DEFAULT '',
    ADD COLUMN scopes       TEXT[]      NOT NULL DEFAULT ARRAY['*'],
    ADD COLUMN last_used_at TIMESTAMPTZ;

-- Existing keys cannot recover their raw prefix; keep a stable hash prefix for
-- display until they are rotated.
UPDATE api_keys SET prefix = LEFT(key_hash, 8) WHERE prefix = '';

-- +goose Down

ALTER TABLE api_keys
    DROP COLUMN IF EXISTS last_used_at,
    DROP COLUMN IF EXISTS scopes,
    DROP COLUMN IF EXISTS prefix;
