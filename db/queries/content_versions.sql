-- Content versions (#34): append-only snapshots of saved queries and metrics.
-- The service writes the entity and its version in one transaction.

-- name: InsertContentVersion :one
INSERT INTO content_versions (
    workspace_id, space_id, entity_type, entity_id, version, change_kind,
    snapshot, author_id, restored_from
)
VALUES (
    @workspace_id::uuid, @space_id::uuid, @entity_type::text, @entity_id::uuid,
    @version::int, @change_kind::text, @snapshot::jsonb,
    sqlc.narg(author_id)::uuid, sqlc.narg(restored_from)::int
)
RETURNING *;

-- name: NextContentVersion :one
SELECT COALESCE(max(version), 0) + 1 AS next
FROM content_versions
WHERE entity_type = @entity_type::text AND entity_id = @entity_id::uuid;

-- name: ListContentVersions :many
SELECT cv.*, u.name AS author_name
FROM content_versions cv
LEFT JOIN users u ON u.id = cv.author_id
WHERE cv.workspace_id = @workspace_id::uuid
  AND cv.entity_type = @entity_type::text
  AND cv.entity_id = @entity_id::uuid
ORDER BY cv.version DESC
LIMIT @lim::int OFFSET @off::int;

-- name: GetContentVersion :one
SELECT cv.*, u.name AS author_name
FROM content_versions cv
LEFT JOIN users u ON u.id = cv.author_id
WHERE cv.workspace_id = @workspace_id::uuid
  AND cv.entity_type = @entity_type::text
  AND cv.entity_id = @entity_id::uuid
  AND cv.version = @version::int;

-- name: LatestContentVersion :one
SELECT * FROM content_versions
WHERE workspace_id = @workspace_id::uuid
  AND entity_type = @entity_type::text
  AND entity_id = @entity_id::uuid
ORDER BY version DESC
LIMIT 1;

-- name: ListDeletedContent :many
-- Entities whose latest delete/restore version is a delete (not restored yet).
WITH latest AS (
    SELECT DISTINCT ON (entity_type, entity_id) *
    FROM content_versions
    WHERE workspace_id = @workspace_id::uuid AND change_kind IN ('delete', 'restore')
    ORDER BY entity_type, entity_id, version DESC
)
SELECT latest.*, u.name AS author_name, s.name AS space_name
FROM latest
LEFT JOIN users u ON u.id = latest.author_id
LEFT JOIN spaces s ON s.id = latest.space_id
WHERE latest.change_kind = 'delete'
  AND (sqlc.narg(entity_type)::text IS NULL OR latest.entity_type = sqlc.narg(entity_type)::text)
ORDER BY latest.created_at DESC
LIMIT @lim::int OFFSET @off::int;

-- name: GetSavedQueryForUpdate :one
SELECT * FROM saved_queries WHERE id = @id::uuid AND workspace_id = @workspace_id::uuid FOR UPDATE;

-- name: GetSemanticMetricForUpdate :one
SELECT * FROM semantic_metrics WHERE id = @id::uuid AND workspace_id = @workspace_id::uuid FOR UPDATE;

-- name: RestoreSavedQuery :one
-- Recreate a deleted saved query with its old id, so links and execution
-- history still line up.
INSERT INTO saved_queries (
    id, workspace_id, space_id, folder_id, created_by, updated_by, name, slug, sql,
    target_binding_id, target_schema, created_at, updated_at
)
VALUES (
    @id::uuid, @workspace_id::uuid, @space_id::uuid, sqlc.narg(folder_id)::uuid, @created_by::uuid,
    sqlc.narg(updated_by)::uuid, @name::text, @slug::text, @sql::text,
    sqlc.narg(target_binding_id)::uuid, sqlc.narg(target_schema)::text, now(), now()
)
RETURNING *;

-- name: RestoreSemanticMetric :one
INSERT INTO semantic_metrics (
    id, workspace_id, space_id, slug, label, description, certified, catalog_id,
    namespace, table_name, binding_id, definition, format_version, owner_user_id,
    updated_by, updated_at
)
VALUES (
    @id::uuid, @workspace_id::uuid, @space_id::uuid, @slug::text, @label::text,
    @description::text, @certified::boolean, @catalog_id::uuid, @namespace::text[],
    @table_name::text, sqlc.narg(binding_id)::uuid, @definition::jsonb,
    @format_version::int, sqlc.narg(owner_user_id)::uuid, @updated_by::uuid, now()
)
RETURNING *;
