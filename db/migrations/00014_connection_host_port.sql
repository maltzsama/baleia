-- +goose Up

ALTER TABLE catalog_connections
    ADD COLUMN host TEXT,
    ADD COLUMN port TEXT;

ALTER TABLE catalogs
    DROP COLUMN trino_catalog_name;

-- +goose Down

ALTER TABLE catalog_connections
    DROP COLUMN IF EXISTS host,
    DROP COLUMN IF EXISTS port;

ALTER TABLE catalogs
    ADD COLUMN trino_catalog_name TEXT NOT NULL DEFAULT '';
