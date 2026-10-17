-- Bundles (#34): deterministic, portable content-as-code. Items are matched by
-- (kind, slug), never by id; omitted items are never deleted.

-- name: ListSpaceFolders :many
SELECT * FROM query_folders WHERE space_id = @space_id::uuid ORDER BY slug;

-- name: ListSpaceSavedQueries :many
SELECT * FROM saved_queries WHERE space_id = @space_id::uuid ORDER BY slug;

-- name: ListSpaceMetrics :many
SELECT * FROM semantic_metrics WHERE space_id = @space_id::uuid ORDER BY slug;

-- name: GetFolderBySlug :one
SELECT * FROM query_folders WHERE space_id = @space_id::uuid AND slug = @slug::text;

-- name: GetSavedQueryBySlug :one
SELECT * FROM saved_queries WHERE workspace_id = @workspace_id::uuid AND slug = @slug::text;

-- name: GetBindingTargetNames :one
SELECT c.name AS catalog_name, e.name AS engine_name
FROM catalog_bindings b
JOIN catalogs c ON c.id = b.catalog_id
JOIN engines e ON e.id = b.engine_id
WHERE b.id = @id::uuid;

-- name: GetCatalogNameByID :one
SELECT name FROM catalogs WHERE id = @id::uuid;

-- name: GetGrantedCatalogByName :one
-- A catalog the workspace may use, by its logical name.
WITH visible AS (
    SELECT c.id, c.name FROM catalogs c
    JOIN catalog_connections cc ON cc.id = c.connection_id
    JOIN workspaces w ON w.id = @workspace_id::uuid
    WHERE w.type = 'personal' AND w.owner_user_id IS NOT NULL AND cc.created_by = w.owner_user_id AND c.enabled
    UNION
    SELECT c.id, c.name FROM workspace_catalog_grants g JOIN catalogs c ON c.id = g.catalog_id
    WHERE g.workspace_id = @workspace_id::uuid AND c.enabled
)
SELECT id FROM visible WHERE name = @name::text;

-- name: UpdateQueryFolder :one
UPDATE query_folders
SET name = @name::text, parent_id = sqlc.narg(parent_id)::uuid
WHERE id = @id::uuid AND workspace_id = @workspace_id::uuid
RETURNING *;
