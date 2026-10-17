-- +goose Up
ALTER TABLE semantic_metrics
    ADD COLUMN certified_by UUID REFERENCES users (id) ON DELETE SET NULL,
    ADD COLUMN certified_at TIMESTAMPTZ;

UPDATE semantic_metrics SET certified_at = updated_at WHERE certified;

-- +goose Down
ALTER TABLE semantic_metrics
    DROP COLUMN IF EXISTS certified_at,
    DROP COLUMN IF EXISTS certified_by;
