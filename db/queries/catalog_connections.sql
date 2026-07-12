-- name: CreateCatalogConnection :one
INSERT INTO catalog_connections (name, type, url, credentials_enc, default_readonly)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetCatalogConnectionByID :one
SELECT * FROM catalog_connections WHERE id = $1;

-- name: ListCatalogConnections :many
SELECT * FROM catalog_connections ORDER BY name;

-- name: UpdateCatalogConnection :one
UPDATE catalog_connections
SET name = $2, url = $3, credentials_enc = $4, default_readonly = $5, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: DeleteCatalogConnection :exec
DELETE FROM catalog_connections WHERE id = $1;

-- name: GrantCatalogAccess :one
INSERT INTO workspace_catalog_access (workspace_id, catalog_id, permission, granted_by)
VALUES ($1, $2, $3, $4)
ON CONFLICT (workspace_id, catalog_id)
DO UPDATE SET permission = $3, granted_by = $4, granted_at = now()
RETURNING *;

-- name: RevokeCatalogAccess :exec
DELETE FROM workspace_catalog_access
WHERE workspace_id = $1 AND catalog_id = $2;

-- name: ListCatalogAccessByWorkspace :many
SELECT wca.*, c.name AS catalog_name, c.trino_catalog_name, c.connection_id, cc.name AS connection_name, cc.type AS connection_type, cc.url AS connection_url
FROM workspace_catalog_access wca
JOIN catalogs c ON c.id = wca.catalog_id
JOIN catalog_connections cc ON cc.id = c.connection_id
WHERE wca.workspace_id = $1
ORDER BY c.name;

-- name: GetCatalogAccess :one
SELECT * FROM workspace_catalog_access
WHERE workspace_id = $1 AND catalog_id = $2;