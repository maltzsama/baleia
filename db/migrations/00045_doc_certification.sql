-- +goose Up
ALTER TABLE semantic_docs
    ADD COLUMN certified BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN certified_by UUID REFERENCES users (id) ON DELETE SET NULL,
    ADD COLUMN certified_at TIMESTAMPTZ;

-- +goose Down
ALTER TABLE semantic_docs
    DROP COLUMN IF EXISTS certified_at,
    DROP COLUMN IF EXISTS certified_by,
    DROP COLUMN IF EXISTS certified;
