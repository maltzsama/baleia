-- +goose Up

-- Every refresh token belongs to a rotation family (#51). The first token of a
-- family uses its own id as family_id; rotated tokens inherit it so reuse
-- detection can revoke the whole family. Rows that predate the column are
-- backfilled to their own id, which is equivalent to one family per token.
ALTER TABLE refresh_tokens ADD COLUMN family_id UUID;
UPDATE refresh_tokens SET family_id = id WHERE family_id IS NULL;
ALTER TABLE refresh_tokens ALTER COLUMN family_id SET NOT NULL;

CREATE INDEX idx_refresh_tokens_family_id ON refresh_tokens(family_id);

-- +goose Down

DROP INDEX IF EXISTS idx_refresh_tokens_family_id;
ALTER TABLE refresh_tokens DROP COLUMN IF EXISTS family_id;
