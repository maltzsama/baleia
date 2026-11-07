package migrations

import (
	"context"

	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(up00067, down00067)
}

const up00067Postgres = `
-- Tables a statement reads and the equi-join predicates it uses (B0, #530,
-- #538). Written after every finished statement and by the one-off backfill.
CREATE TABLE statement_references (
    statement_id UUID NOT NULL REFERENCES query_statements (id) ON DELETE CASCADE,
    workspace_id UUID NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    -- The target's catalog. NULL when the target had none.
    catalog_id   UUID REFERENCES catalogs (id) ON DELETE SET NULL,
    namespace    TEXT NOT NULL DEFAULT '',
    table_name   TEXT NOT NULL,
    alias        TEXT NOT NULL DEFAULT '',
    kind         TEXT NOT NULL DEFAULT 'read' CHECK (kind IN ('read', 'write')),
    PRIMARY KEY (statement_id, namespace, table_name, alias)
);

CREATE INDEX statement_references_workspace_table
    ON statement_references (workspace_id, namespace, table_name);
CREATE INDEX statement_references_statement ON statement_references (statement_id);

CREATE TABLE statement_joins (
    statement_id UUID NOT NULL REFERENCES query_statements (id) ON DELETE CASCADE,
    workspace_id UUID NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    left_alias   TEXT NOT NULL,
    left_column  TEXT NOT NULL,
    right_alias  TEXT NOT NULL,
    right_column TEXT NOT NULL,
    PRIMARY KEY (statement_id, left_alias, left_column, right_alias, right_column)
);

CREATE INDEX statement_joins_workspace ON statement_joins (workspace_id);
`


const up00067MySQL = `
CREATE TABLE statement_references (
    statement_id CHAR(36) NOT NULL REFERENCES query_statements (id) ON DELETE CASCADE,
    workspace_id CHAR(36) NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    
    catalog_id   CHAR(36) REFERENCES catalogs (id) ON DELETE SET NULL,
    namespace    TEXT NOT NULL DEFAULT '',
    table_name   TEXT NOT NULL,
    alias        TEXT NOT NULL DEFAULT '',
    kind         TEXT NOT NULL DEFAULT 'read' CHECK (kind IN ('read', 'write')),
    PRIMARY KEY (statement_id, namespace, table_name, alias)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE INDEX statement_references_workspace_table
    ON statement_references (workspace_id, namespace, table_name);
CREATE INDEX statement_references_statement ON statement_references (statement_id);

CREATE TABLE statement_joins (
    statement_id CHAR(36) NOT NULL REFERENCES query_statements (id) ON DELETE CASCADE,
    workspace_id CHAR(36) NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    left_alias   TEXT NOT NULL,
    left_column  TEXT NOT NULL,
    right_alias  TEXT NOT NULL,
    right_column TEXT NOT NULL,
    PRIMARY KEY (statement_id, left_alias, left_column, right_alias, right_column)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE INDEX statement_joins_workspace ON statement_joins (workspace_id);
`


const down00067Postgres = `
DROP TABLE IF EXISTS statement_joins;
DROP TABLE IF EXISTS statement_references;
`


const down00067MySQL = `
DROP TABLE IF EXISTS statement_joins;
DROP TABLE IF EXISTS statement_references;
`


func up00067(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, up00067Postgres, up00067MySQL)
}

func down00067(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, down00067Postgres, down00067MySQL)
}
