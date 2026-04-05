-- +goose Up

CREATE TABLE catalog_connections (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name             STRING        NOT NULL UNIQUE,
    type             STRING        NOT NULL CHECK (type IN ('polaris', 'nessie', 'glue', 'hive')),
    url              STRING        NOT NULL,
    credentials_enc  TEXT,
    default_readonly BOOL          NOT NULL DEFAULT true,
    created_at       TIMESTAMPTZ   NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ   NOT NULL DEFAULT now()
);

CREATE TABLE workspace_catalog_access (
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id          UUID          NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    catalog_connection_id UUID          NOT NULL REFERENCES catalog_connections (id) ON DELETE CASCADE,
    permission            STRING        NOT NULL CHECK (permission IN ('read', 'write')),
    granted_by            UUID          NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    granted_at            TIMESTAMPTZ   NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, catalog_connection_id)
);

CREATE INDEX idx_workspace_catalog_access_workspace ON workspace_catalog_access (workspace_id);
CREATE INDEX idx_workspace_catalog_access_catalog   ON workspace_catalog_access (catalog_connection_id);

-- +goose Down

DROP TABLE IF EXISTS workspace_catalog_access;
DROP TABLE IF EXISTS catalog_connections;