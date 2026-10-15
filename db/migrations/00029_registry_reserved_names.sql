-- +goose Up
-- The registry refuses the names Trino and the plugin reserve (#39), as the
-- catalogs table does since 00019; contract/reserved_catalog_names.json of
-- the pinned plugin release is the list, checked by internal/contract.
ALTER TABLE trino_catalog_registry
    ADD CONSTRAINT registry_catalog_name_not_reserved
        CHECK (catalog_name NOT IN ('system', 'jmx', 'tpch', 'tpcds', 'memory', 'env', 'file'));

-- +goose Down
ALTER TABLE trino_catalog_registry DROP CONSTRAINT registry_catalog_name_not_reserved;
