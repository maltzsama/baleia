-- +goose Up

-- Connection tests bind a short-lived catalog into Trino (#12). Its registry
-- row expires: the test drops it and hard-deletes the row (it holds real
-- credentials), and the ephemeral-catalog-sweeper does the same for rows a
-- crashed test left behind. NULL = a permanent catalog. The plugin selects
-- explicit columns, so it ignores this one.
ALTER TABLE trino_catalog_registry ADD COLUMN expires_at TIMESTAMPTZ;
CREATE INDEX trino_catalog_registry_expires ON trino_catalog_registry (expires_at) WHERE expires_at IS NOT NULL;

-- Test catalogs the old test path left behind when it failed midway: the
-- sweeper drops them on its first run.
UPDATE trino_catalog_registry SET expires_at = now() WHERE catalog_name LIKE 'baleia\_test\_%';

-- +goose Down

DROP INDEX IF EXISTS trino_catalog_registry_expires;
ALTER TABLE trino_catalog_registry DROP COLUMN IF EXISTS expires_at;
