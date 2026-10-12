-- Seed for the T1-T9 acceptance tests of the baleia-trino-catalog-store plugin (CR-014 rev.3).
-- Idempotent: safe to run multiple times. Not a migration — run it manually
-- after compose-up when validating the plugin integration.
--
-- Usage:
--   make seed-tpch
-- or:
--   docker compose exec -T postgres psql -U baleia -d baleia < db/seed/tpch_test.sql
--
-- The `tpch` connector is built into Trino and needs no external service,
-- which is why it is used to validate that the plugin reads the registry at boot.

INSERT INTO trino_catalog_registry
    (cluster_id, catalog_name, connector_name, properties, enabled, sync_status, updated_by)
VALUES (
    (SELECT id FROM trino_clusters WHERE name = 'default'),
    'tpch_test',
    'tpch',
    '{"tpch.splits-per-node": "4"}'::jsonb,
    TRUE,
    'pending',
    'baleia'
)
ON CONFLICT (cluster_id, catalog_name) DO NOTHING;