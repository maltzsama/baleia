-- +goose Up
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

-- +goose Down
DROP TABLE IF EXISTS statement_joins;
DROP TABLE IF EXISTS statement_references;
