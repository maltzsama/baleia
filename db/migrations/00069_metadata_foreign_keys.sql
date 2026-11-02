-- +goose Up
-- Foreign keys of the physical metadata snapshot (B1, #554): one column of a
-- table referencing one column of another. Read from the database providers
-- (PostgreSQL, MySQL, SQL Server, Oracle) in the same pass as the columns.
-- The referenced table is stored by name because a table's keys are written
-- before the whole catalog is walked; a read resolves it against
-- metadata_tables.
CREATE TABLE metadata_foreign_keys (
    catalog_id    UUID        NOT NULL REFERENCES catalogs (id) ON DELETE CASCADE,
    table_id      UUID        NOT NULL REFERENCES metadata_tables (id) ON DELETE CASCADE,
    column_name   TEXT        NOT NULL,
    ref_namespace TEXT[]      NOT NULL,
    ref_table     TEXT        NOT NULL,
    ref_column    TEXT        NOT NULL,
    last_seen_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (table_id, column_name, ref_namespace, ref_table, ref_column)
);

CREATE INDEX metadata_foreign_keys_catalog ON metadata_foreign_keys (catalog_id);
CREATE INDEX metadata_foreign_keys_ref ON metadata_foreign_keys (catalog_id, ref_namespace, ref_table);

-- +goose Down
DROP TABLE IF EXISTS metadata_foreign_keys;
