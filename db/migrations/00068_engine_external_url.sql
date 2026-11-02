-- +goose Up
-- The address BI clients reach, which may differ from the in-cluster URL
-- (#533, #560). Nullable: an engine without it cannot be opened from a BI tool.
ALTER TABLE engines ADD COLUMN external_url TEXT;

-- +goose Down
ALTER TABLE engines DROP COLUMN IF EXISTS external_url;
