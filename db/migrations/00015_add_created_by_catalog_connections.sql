-- +goose Up
ALTER TABLE catalog_connections ADD COLUMN created_by UUID REFERENCES users(id) ON DELETE SET NULL;

-- +goose Down
ALTER TABLE catalog_connections DROP COLUMN created_by;
