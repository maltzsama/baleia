-- +goose Up

CREATE TABLE catalogs (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    connection_id      UUID NOT NULL REFERENCES catalog_connections(id) ON DELETE CASCADE,
    name               TEXT NOT NULL,
    description        TEXT,
    trino_catalog_name TEXT NOT NULL,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (connection_id, name),
    UNIQUE (trino_catalog_name)
);

DROP TABLE IF EXISTS workspace_catalog_access;

CREATE TABLE workspace_catalog_access (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    catalog_id   UUID NOT NULL REFERENCES catalogs(id) ON DELETE CASCADE,
    permission   TEXT NOT NULL DEFAULT 'read',
    granted_by   UUID NOT NULL REFERENCES users(id),
    granted_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, catalog_id)
);

-- +goose Down

DROP TABLE IF EXISTS workspace_catalog_access;
DROP TABLE IF EXISTS catalogs;
