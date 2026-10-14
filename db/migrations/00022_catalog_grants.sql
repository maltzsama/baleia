-- +goose Up

-- Grants of a catalog to a workspace. The previous table (workspace_catalog_access,
-- keyed by connection) was dropped in 00017, so there is nothing to carry over.
CREATE TABLE workspace_catalog_grants (
    workspace_id UUID        NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    catalog_id   UUID        NOT NULL REFERENCES catalogs (id)   ON DELETE CASCADE,
    permission   TEXT        NOT NULL CHECK (permission IN ('read', 'write')),
    granted_by   UUID        NOT NULL REFERENCES users (id)      ON DELETE RESTRICT,
    granted_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, catalog_id)
);

CREATE INDEX idx_workspace_catalog_grants_catalog ON workspace_catalog_grants (catalog_id);

-- +goose Down

DROP TABLE IF EXISTS workspace_catalog_grants;
