-- +goose Up
-- #654: the grant's read/write permission and the never-enforced
-- default_readonly flag are no longer read or written (#651). They stayed
-- through 0.6.1 and 0.6.2 so a pod from the previous release could finish its
-- rolling update while still naming them; now they are dropped.
ALTER TABLE workspace_catalog_grants DROP COLUMN permission;
ALTER TABLE catalog_connections DROP COLUMN default_readonly;

-- +goose Down
ALTER TABLE workspace_catalog_grants
    ADD COLUMN permission TEXT NOT NULL DEFAULT 'write' CHECK (permission IN ('read', 'write'));
ALTER TABLE catalog_connections
    ADD COLUMN default_readonly BOOLEAN NOT NULL DEFAULT true;
