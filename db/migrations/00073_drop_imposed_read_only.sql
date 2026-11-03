-- +goose Up
-- #651: Baleia no longer imposes read-only work. A catalog linked to a
-- workspace stays reachable by that workspace's members only; what they may
-- read or write in it is decided by the engine and the catalog manager, or by
-- the connection's service account. The grant's read/write permission, the
-- connection's assertion that its credentials are read-only (which existed
-- only to allow read-only grants on engines without enforcement) and the
-- never-enforced default_readonly flag go away.

ALTER TABLE workspace_catalog_grants DROP COLUMN IF EXISTS permission;

ALTER TABLE catalog_connections
    DROP COLUMN IF EXISTS credentials_read_only,
    DROP COLUMN IF EXISTS default_readonly;

-- +goose Down
-- Grants come back as write, so restoring the column reintroduces no
-- read-only sessions.
ALTER TABLE workspace_catalog_grants
    ADD COLUMN IF NOT EXISTS permission TEXT NOT NULL DEFAULT 'write'
        CHECK (permission IN ('read', 'write'));

ALTER TABLE catalog_connections
    ADD COLUMN IF NOT EXISTS credentials_read_only BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS default_readonly BOOLEAN NOT NULL DEFAULT true;
