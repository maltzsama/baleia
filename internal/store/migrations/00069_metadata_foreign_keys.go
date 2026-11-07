package migrations

import (
	"context"

	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(up00069, down00069)
}

const up00069Postgres = `
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
`

const up00069MySQL = `
CREATE TABLE metadata_foreign_keys (
    catalog_id    CHAR(36)        NOT NULL REFERENCES catalogs (id) ON DELETE CASCADE,
    table_id      CHAR(36)        NOT NULL REFERENCES metadata_tables (id) ON DELETE CASCADE,
    column_name   TEXT        NOT NULL,
    ref_namespace JSON      NOT NULL,
    ref_table     TEXT        NOT NULL,
    ref_column    TEXT        NOT NULL,
    last_seen_at  DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (table_id, column_name, ref_namespace, ref_table, ref_column)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE INDEX metadata_foreign_keys_catalog ON metadata_foreign_keys (catalog_id);
CREATE INDEX metadata_foreign_keys_ref ON metadata_foreign_keys (catalog_id, ref_namespace, ref_table);
`

const down00069Postgres = `
DROP TABLE IF EXISTS metadata_foreign_keys;
`

const down00069MySQL = `
DROP TABLE IF EXISTS metadata_foreign_keys;
`

func up00069(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, up00069Postgres, up00069MySQL)
}

func down00069(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, down00069Postgres, down00069MySQL)
}
