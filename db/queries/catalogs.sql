-- name: CreateCatalog :one
INSERT INTO catalogs (connection_id, name, description)
VALUES ($1, $2, $3)
RETURNING *;

-- name: GetCatalogByID :one
SELECT * FROM catalogs WHERE id = $1;

-- name: ListCatalogsByConnection :many
SELECT * FROM catalogs WHERE connection_id = $1 ORDER BY name;

-- name: UpdateCatalog :one
UPDATE catalogs
SET name = $2, description = $3, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: DeleteCatalog :exec
DELETE FROM catalogs WHERE id = $1;
