-- +goose Up
-- #694: an immutable owner marker for the platform's own connections (the
-- default Trino cluster, the operator's catalog managers). created_by IS NULL
-- alone cannot tell a system-owned connection from one whose owner was deleted
-- (00015 uses ON DELETE SET NULL), and only the former may reach a private
-- address through the managed dialer (#609).
ALTER TABLE catalog_connections ADD COLUMN system_owned BOOLEAN NOT NULL DEFAULT false;

-- Existing rows keep today's behavior: every created_by IS NULL row was
-- treated as system-owned before the marker. New orphans (created_by nulled by
-- a user deletion) keep false and stay on the strict dialer.
UPDATE catalog_connections SET system_owned = true WHERE created_by IS NULL;

-- +goose Down
ALTER TABLE catalog_connections DROP COLUMN system_owned;
