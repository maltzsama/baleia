-- name: CreateCatalogConnection :one
INSERT INTO catalog_connections (name, type, url, credentials_enc, default_readonly, created_by)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetCatalogConnectionByID :one
SELECT * FROM catalog_connections WHERE id = $1;

-- name: ListCatalogConnections :many
SELECT * FROM catalog_connections WHERE created_by = $1 ORDER BY name;

-- name: UpdateCatalogConnection :one
UPDATE catalog_connections
SET name = $2, url = $3, credentials_enc = $4, default_readonly = $5, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: DeleteCatalogConnection :exec
DELETE FROM catalog_connections WHERE id = $1;