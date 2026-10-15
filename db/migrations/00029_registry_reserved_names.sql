-- +goose NO TRANSACTION
-- +goose Up
-- The registry refuses the names Trino and the plugin reserve (#39), as the
-- catalogs table does since 00019; contract/reserved_catalog_names.json of
-- the pinned plugin release is the list, checked by internal/contract.
-- Added NOT VALID and validated in its own statement (no transaction), so the
-- scan of existing rows does not hold the ACCESS EXCLUSIVE lock; the DROP
-- makes a rerun after a failed validation start over.
ALTER TABLE trino_catalog_registry DROP CONSTRAINT IF EXISTS registry_catalog_name_not_reserved;
ALTER TABLE trino_catalog_registry
    ADD CONSTRAINT registry_catalog_name_not_reserved
        CHECK (catalog_name NOT IN ('system', 'jmx', 'tpch', 'tpcds', 'memory', 'env', 'file')) NOT VALID;
ALTER TABLE trino_catalog_registry VALIDATE CONSTRAINT registry_catalog_name_not_reserved;

-- +goose Down
ALTER TABLE trino_catalog_registry DROP CONSTRAINT IF EXISTS registry_catalog_name_not_reserved;
