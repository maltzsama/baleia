-- +goose Up
-- #782: the system_owned marker existed only to route system-owned connections
-- to the managed dialer (RFC1918 open). The dialer is now fully config-driven
-- (BALEIA_CONNECTIONS_DENY_CIDRS / _ALLOW_CIDRS), so the split is redundant:
-- every connection uses the same dialer and the deployer decides the policy.
ALTER TABLE catalog_connections DROP COLUMN system_owned;

-- +goose Down
ALTER TABLE catalog_connections ADD COLUMN system_owned BOOLEAN NOT NULL DEFAULT false;
-- Restore the marker the way 00078 first derived it, so an older binary that
-- reads system_owned still treats the bootstrap connection as platform-owned
-- after a rollback.
UPDATE catalog_connections SET system_owned = true WHERE created_by IS NULL;
