-- +goose Up
-- The baleia-trino-catalog-store plugin (v0.3.0+) reserves "env" and "file" as
-- catalog names because they collide with the @baleia-secret[<origin>:<key>]
-- origin schemes. Extend the CHECK to match the plugin's CatalogRow validation.
ALTER TABLE catalogs
    DROP CONSTRAINT catalogs_trino_name_not_reserved;

ALTER TABLE catalogs
    ADD CONSTRAINT catalogs_trino_name_not_reserved
    CHECK (trino_catalog_name NOT IN ('system', 'jmx', 'tpch', 'tpcds', 'memory', 'env', 'file'));

-- +goose Down
ALTER TABLE catalogs
    DROP CONSTRAINT catalogs_trino_name_not_reserved;

ALTER TABLE catalogs
    ADD CONSTRAINT catalogs_trino_name_not_reserved
    CHECK (trino_catalog_name NOT IN ('system', 'jmx', 'tpch', 'tpcds', 'memory'));
