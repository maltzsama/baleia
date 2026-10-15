-- The statements the plugin issues against the registry (Database.java).
-- ContractTest fails when they differ; Baleia prepares each one as baleia_trino
-- against its migrated schema. Change both together, then release.

-- name: SELECT_ALL
SELECT r.catalog_name, r.connector_name, r.properties::text FROM trino_catalog_registry r JOIN trino_clusters c ON c.id = r.cluster_id WHERE c.name = ? AND r.enabled ORDER BY r.catalog_name;

-- name: SELECT_ONE
SELECT r.properties::text FROM trino_catalog_registry r JOIN trino_clusters c ON c.id = r.cluster_id WHERE c.name = ? AND r.catalog_name = ? AND r.enabled;

-- name: SELECT_CLUSTER
SELECT 1 FROM trino_clusters WHERE name = ?;

-- name: UPSERT
INSERT INTO trino_catalog_registry (cluster_id, catalog_name, connector_name, properties,  catalog_version, enabled, sync_status, sync_error, updated_by, updated_at) VALUES ((SELECT id FROM trino_clusters WHERE name = ?),         ?, ?, ?::jsonb, ?, true, 'synced', NULL, 'trino', now()) ON CONFLICT (cluster_id, catalog_name) DO UPDATE SET  connector_name  = EXCLUDED.connector_name,  properties      = EXCLUDED.properties,  catalog_version = EXCLUDED.catalog_version,  enabled         = true,  sync_status     = 'synced',  sync_error      = NULL,  updated_by      = 'trino',  updated_at      = now();

-- name: SOFT_DELETE
UPDATE trino_catalog_registry r SET enabled = false, updated_by = 'trino', updated_at = now() FROM trino_clusters c WHERE c.id = r.cluster_id AND c.name = ? AND r.catalog_name = ?;

-- name: MARK_ERROR
UPDATE trino_catalog_registry r SET sync_status = 'error', sync_error = ?, updated_by = 'trino', updated_at = now() FROM trino_clusters c WHERE c.id = r.cluster_id AND c.name = ? AND r.catalog_name = ?;

-- name: CLEAR_ERROR
UPDATE trino_catalog_registry r SET sync_status = 'synced', sync_error = NULL, updated_by = 'trino', updated_at = now() FROM trino_clusters c WHERE c.id = r.cluster_id AND c.name = ? AND r.catalog_name = ? AND r.sync_status = 'error';
