-- +goose Up

ALTER TABLE catalog_connections
    DROP CONSTRAINT catalog_connections_type_check;

ALTER TABLE catalog_connections
    ADD CONSTRAINT catalog_connections_type_check
    CHECK (type IN ('polaris', 'nessie', 'glue', 'hive', 'iceberg', 'postgresql', 'mysql'));

-- +goose Down

ALTER TABLE catalog_connections
    DROP CONSTRAINT catalog_connections_type_check;

ALTER TABLE catalog_connections
    ADD CONSTRAINT catalog_connections_type_check
    CHECK (type IN ('polaris', 'nessie', 'glue', 'hive'));
