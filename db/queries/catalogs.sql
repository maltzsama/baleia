-- name: GetDefaultTrinoCluster :one
SELECT * FROM trino_clusters WHERE name = 'default';

-- name: GetTrinoClusterByID :one
SELECT * FROM trino_clusters WHERE id = $1;

-- name: CreateCatalog :one
INSERT INTO catalogs (
    connection_id, name, description, trino_catalog_name,
    cluster_id, warehouse, connector, extra_props
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: GetCatalogByID :one
SELECT * FROM catalogs WHERE id = $1;

-- name: GetCatalogByTrinoName :one
SELECT * FROM catalogs WHERE cluster_id = $1 AND trino_catalog_name = $2;

-- name: ListCatalogsByConnection :many
SELECT * FROM catalogs WHERE connection_id = $1 ORDER BY name;

-- name: ListEnabledCatalogs :many
SELECT * FROM catalogs WHERE enabled ORDER BY name;

-- name: ListEnabledIcebergCatalogs :many
-- Enabled catalogs backed by an Iceberg-family catalog manager (Polaris,
-- Iceberg REST, Nessie), used by the Lens table-health fan-out.
SELECT c.id, c.connection_id, c.name, c.trino_catalog_name
FROM catalogs c
JOIN catalog_connections cc ON cc.id = c.connection_id
WHERE c.enabled AND cc.type IN ('polaris', 'iceberg', 'nessie')
ORDER BY c.name;

-- name: UpdateCatalog :one
UPDATE catalogs SET
    name = $2,
    description = $3,
    warehouse = $4,
    connector = $5,
    extra_props = $6,
    trino_catalog_name = $7,
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

-- name: MarkTrinoCatalogRegistryError :exec
UPDATE trino_catalog_registry
   SET sync_status = 'error', sync_error = $3, updated_by = 'baleia', updated_at = now()
 WHERE cluster_id = $1 AND catalog_name = $2;