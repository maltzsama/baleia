-- name: GetDefaultTrinoCluster :one
SELECT * FROM trino_clusters WHERE name = 'default';

-- name: GetTrinoClusterByID :one
SELECT * FROM trino_clusters WHERE id = $1;

-- name: CreateCatalog :one
-- A source catalog's source_ref is its warehouse (or database).
INSERT INTO catalogs (
    connection_id, name, description, trino_catalog_name,
    cluster_id, warehouse, connector, extra_props, source_ref
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $6)
RETURNING *;

-- name: GetCatalogByID :one
SELECT * FROM catalogs WHERE id = $1;

-- name: GetCatalogByTrinoName :one
SELECT * FROM catalogs WHERE cluster_id = $1 AND trino_catalog_name = sqlc.arg('trino_catalog_name')::text;

-- name: ListCatalogsByConnection :many
SELECT * FROM catalogs WHERE connection_id = $1 ORDER BY name;

-- name: ListEnabledCatalogs :many
SELECT * FROM catalogs WHERE enabled AND origin = 'source' ORDER BY name;

-- name: ListEnabledIcebergCatalogs :many
-- Enabled catalogs backed by an Iceberg-family catalog manager (Polaris,
-- Iceberg REST, Nessie), used by the Lens table-health fan-out.
SELECT c.id, c.connection_id, c.name, COALESCE(c.trino_catalog_name, '')::text AS trino_catalog_name
FROM catalogs c
JOIN catalog_connections cc ON cc.id = c.connection_id
WHERE c.enabled AND c.origin = 'source' AND cc.type IN ('polaris', 'iceberg', 'nessie')
ORDER BY c.name;

-- name: UpdateCatalog :one
UPDATE catalogs SET
    name = $2,
    description = $3,
    warehouse = $4,
    connector = $5,
    extra_props = $6,
    trino_catalog_name = $7,
    source_ref = CASE WHEN origin = 'source' THEN $4 ELSE source_ref END,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: SetCatalogEnabled :exec
UPDATE catalogs SET enabled = $2, updated_at = now() WHERE id = $1;

-- name: DeleteCatalog :exec
DELETE FROM catalogs WHERE id = $1;

-- name: UpsertTrinoCatalogRegistry :exec
INSERT INTO trino_catalog_registry
    (cluster_id, catalog_name, connector_name, properties,
     catalog_version, enabled, sync_status, updated_by)
VALUES ($1, $2, $3, $4::jsonb, $5, TRUE, 'pending', 'baleia')
ON CONFLICT (cluster_id, catalog_name) DO UPDATE SET
    connector_name  = EXCLUDED.connector_name,
    properties      = EXCLUDED.properties,
    catalog_version = EXCLUDED.catalog_version,
    enabled         = TRUE,
    sync_status     = 'pending',
    sync_error      = NULL,
    updated_by      = 'baleia',
    updated_at      = now();

-- name: SoftDeleteTrinoCatalogRegistry :exec
UPDATE trino_catalog_registry
   SET enabled = FALSE, updated_by = 'baleia', updated_at = now()
 WHERE cluster_id = $1 AND catalog_name = $2;

-- name: GetTrinoCatalogRegistryState :one
SELECT catalog_version, enabled, sync_status, updated_by
  FROM trino_catalog_registry
 WHERE cluster_id = $1 AND catalog_name = $2;

-- name: InsertEphemeralRegistryRow :exec
-- A connection test's short-lived catalog (#12): enabled (the plugin
-- resolves placeholders only from enabled rows) and expiring.
INSERT INTO trino_catalog_registry
    (cluster_id, catalog_name, connector_name, properties, catalog_version, enabled, sync_status, updated_by, expires_at)
VALUES ($1, $2, $3, $4::jsonb, $5, TRUE, 'pending', 'baleia', now() + @ttl::interval);

-- name: DeleteEphemeralRegistryRow :exec
-- Hard delete: the row holds real credentials. Permanent rows are never
-- matched.
DELETE FROM trino_catalog_registry
WHERE cluster_id = $1 AND catalog_name = $2 AND expires_at IS NOT NULL;

-- name: ListExpiredEphemeralRegistryRows :many
SELECT cluster_id, catalog_name FROM trino_catalog_registry
WHERE expires_at IS NOT NULL AND expires_at < now();

-- name: ListVisibleCatalogs :many
-- Every logical catalog the caller may view: all for an admin, the caller's
-- connections, and catalogs granted to a workspace the caller belongs to (#109).
SELECT DISTINCT c.* FROM catalogs c
JOIN catalog_connections cc ON cc.id = c.connection_id
WHERE sqlc.arg(is_admin)::bool
   OR cc.created_by = sqlc.arg(user_id)
   OR EXISTS (
        SELECT 1 FROM workspace_catalog_grants g
        JOIN workspaces w ON w.id = g.workspace_id
        LEFT JOIN team_members tm ON tm.team_id = w.owner_team_id AND tm.user_id = sqlc.arg(user_id)
        WHERE g.catalog_id = c.id
          AND (w.owner_user_id = sqlc.arg(user_id) OR tm.user_id = sqlc.arg(user_id)))
ORDER BY c.name;

-- name: CountNamespacesByCatalog :one
SELECT count(*)::bigint FROM metadata_namespaces WHERE catalog_id = $1 AND removed_at IS NULL;

-- name: CountTablesByCatalog :one
SELECT count(*)::bigint FROM metadata_tables WHERE catalog_id = $1 AND removed_at IS NULL;

-- name: CountCatalogImpact :one
SELECT
    (SELECT count(*) FROM catalog_bindings b WHERE b.catalog_id = $1)::bigint AS bindings,
    (SELECT count(*) FROM workspace_catalog_grants g WHERE g.catalog_id = $1)::bigint AS grants,
    (SELECT count(*) FROM saved_queries s
      JOIN catalog_bindings b ON b.id = s.target_binding_id WHERE b.catalog_id = $1)::bigint AS saved_queries_targeting,
    (SELECT count(*) FROM semantic_metrics m WHERE m.catalog_id = $1)::bigint AS metrics,
    0::bigint AS docs,
    (SELECT count(*) FROM query_executions e
      JOIN catalog_bindings b ON b.id = e.target_binding_id
      WHERE b.catalog_id = $1 AND e.status = 'running')::bigint AS running_executions;

-- name: CountConnectionImpact :one
SELECT
    (SELECT count(*) FROM catalogs c WHERE c.connection_id = $1)::bigint AS catalogs,
    (SELECT count(*) FROM catalog_bindings b
      JOIN catalogs c ON c.id = b.catalog_id WHERE c.connection_id = $1)::bigint AS bindings,
    (SELECT count(*) FROM query_executions e
      JOIN catalog_bindings b ON b.id = e.target_binding_id
      JOIN catalogs c ON c.id = b.catalog_id
      WHERE c.connection_id = $1 AND e.status = 'running')::bigint AS running_executions;

-- name: ListCatalogGrants :many
SELECT g.workspace_id, w.name AS workspace_name, w.type AS workspace_type,
       g.permission, g.granted_by, g.granted_at
FROM workspace_catalog_grants g
JOIN workspaces w ON w.id = g.workspace_id
WHERE g.catalog_id = $1
ORDER BY w.name;

-- name: UpdateCatalogMeta :one
UPDATE catalogs
SET name = $2, description = $3, updated_at = now()
WHERE id = $1
RETURNING *;
