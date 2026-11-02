-- Lineage (#558, B4): the tables a model, metric or saved query reads, and
-- the content that reads a table. Name resolution is best-effort: a reference
-- whose table no longer exists simply contributes no edge.

-- name: GetMetadataTableByRef :one
-- A live table by its natural key, to resolve a content reference.
SELECT id, catalog_id, namespace, name, kind
FROM metadata_tables
WHERE catalog_id = @catalog_id::uuid
  AND namespace = @namespace::text[]
  AND name = @name::text
  AND removed_at IS NULL;

-- name: ListLineageModels :many
-- The models the caller can read in one workspace (#40 space role).
SELECT id, workspace_id, space_id, slug, label, catalog_id, definition, updated_at
FROM semantic_models
WHERE workspace_id = @workspace_id::uuid
  AND baleia_effective_space_role(workspace_id, space_id, @user_id::uuid) IS NOT NULL;

-- name: ListLineageMetrics :many
-- The metrics the caller can read in one workspace (#40 space role).
SELECT id, workspace_id, space_id, slug, label, catalog_id, namespace, table_name, updated_at
FROM semantic_metrics
WHERE workspace_id = @workspace_id::uuid
  AND baleia_effective_space_role(workspace_id, space_id, @user_id::uuid) IS NOT NULL;

-- name: ListLineageSavedQueries :many
-- The saved queries the caller can read in one workspace, with the catalog
-- their target binding points at (NULL when they have no target).
SELECT q.id, q.workspace_id, q.space_id, q.slug, q.name, q.sql, q.target_schema,
       b.catalog_id AS target_catalog_id
FROM saved_queries q
LEFT JOIN catalog_bindings b ON b.id = q.target_binding_id
WHERE q.workspace_id = @workspace_id::uuid
  AND baleia_effective_space_role(q.workspace_id, q.space_id, @user_id::uuid) IS NOT NULL;

-- name: ListLineageBrokenModels :many
-- Every model, to detect a base table or column that no longer exists.
SELECT id, workspace_id, space_id, slug, label, catalog_id, definition, updated_at
FROM semantic_models;

-- name: ListLineageBrokenMetrics :many
-- Every metric, to detect a base table that no longer exists.
SELECT id, workspace_id, space_id, slug, label, catalog_id, namespace, table_name, updated_at
FROM semantic_metrics;
