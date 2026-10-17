-- Semantic layer v1 (#33): docs keyed by natural key, simple metrics keyed by
-- portable slug. Docs are workspace-scoped; the catalog must be granted to the
-- workspace (#16). Metrics become content items in #40 and are versioned in #34.

-- ── Docs ────────────────────────────────────────────────────────────────────────

-- name: ListSemanticDocsForTable :many
-- Every doc of one table (table-level and per-column), optionally narrowed to
-- one column. The natural key is catalog + namespace + table [+ column].
SELECT * FROM semantic_docs
WHERE workspace_id = @workspace_id::uuid
  AND catalog_id = @catalog_id::uuid
  AND namespace = @namespace::text[]
  AND table_name = @table_name::text
  AND (sqlc.narg(column_name)::text IS NULL OR column_name = sqlc.narg(column_name)::text)
ORDER BY column_name NULLS FIRST;

-- name: GetSemanticDoc :one
SELECT * FROM semantic_docs
WHERE workspace_id = @workspace_id::uuid
  AND catalog_id = @catalog_id::uuid
  AND namespace = @namespace::text[]
  AND table_name = @table_name::text
  AND column_name IS NOT DISTINCT FROM sqlc.narg(column_name)::text;

-- name: UpsertSemanticDoc :one
INSERT INTO semantic_docs (
    workspace_id, catalog_id, namespace, table_name, column_name,
    description, tags, owner_user_id, updated_by
)
VALUES (
    @workspace_id::uuid, @catalog_id::uuid, @namespace::text[], @table_name::text,
    sqlc.narg(column_name)::text, @description::text, @tags::text[],
    sqlc.narg(owner_user_id)::uuid, @updated_by::uuid
)
ON CONFLICT (workspace_id, catalog_id, namespace, table_name, column_name) DO UPDATE
SET description = EXCLUDED.description,
    tags = EXCLUDED.tags,
    owner_user_id = EXCLUDED.owner_user_id,
    updated_by = EXCLUDED.updated_by,
    updated_at = now()
RETURNING *;

-- name: DeleteSemanticDoc :execrows
DELETE FROM semantic_docs
WHERE workspace_id = @workspace_id::uuid
  AND catalog_id = @catalog_id::uuid
  AND namespace = @namespace::text[]
  AND table_name = @table_name::text
  AND column_name IS NOT DISTINCT FROM sqlc.narg(column_name)::text;

-- name: SearchSemanticDocs :many
-- Trigram search over docs of catalogs the workspace may use. A tag narrows
-- the result to docs carrying it.
WITH visible AS (
    SELECT c.id FROM catalogs c
    JOIN catalog_connections cc ON cc.id = c.connection_id
    JOIN workspaces w ON w.id = @workspace_id::uuid
    WHERE w.type = 'personal' AND w.owner_user_id IS NOT NULL AND cc.created_by = w.owner_user_id AND c.enabled
    UNION
    SELECT g.catalog_id FROM workspace_catalog_grants g
    JOIN catalogs c ON c.id = g.catalog_id
    WHERE g.workspace_id = @workspace_id::uuid AND c.enabled
)
SELECT d.*, greatest(
    similarity(d.description, @q::text),
    similarity(d.table_name, @q::text),
    similarity(coalesce(d.column_name, ''), @q::text)
)::float8 AS score
FROM semantic_docs d
JOIN visible v ON v.id = d.catalog_id
WHERE d.workspace_id = @workspace_id::uuid
  AND (sqlc.narg(tag)::text IS NULL OR sqlc.narg(tag)::text = ANY(d.tags))
  AND (
    d.description ILIKE '%' || @q::text || '%'
    OR d.table_name ILIKE '%' || @q::text || '%'
    OR coalesce(d.column_name, '') ILIKE '%' || @q::text || '%'
    OR similarity(d.description, @q::text) > 0.1
  )
ORDER BY score DESC, d.updated_at DESC
LIMIT @lim::int;

-- ── Metrics ─────────────────────────────────────────────────────────────────────

-- name: ListSemanticMetrics :many
SELECT * FROM semantic_metrics
WHERE workspace_id = @workspace_id::uuid
ORDER BY label;

-- name: GetSemanticMetric :one
SELECT * FROM semantic_metrics
WHERE workspace_id = @workspace_id::uuid AND id = @id::uuid;

-- name: GetSemanticMetricBySlug :one
SELECT * FROM semantic_metrics
WHERE workspace_id = @workspace_id::uuid AND slug = @slug::text;

-- name: CreateSemanticMetric :one
INSERT INTO semantic_metrics (
    workspace_id, slug, label, description, certified,
    catalog_id, namespace, table_name, binding_id, definition, format_version,
    owner_user_id, updated_by
)
VALUES (
    @workspace_id::uuid, @slug::text, @label::text, @description::text, @certified::boolean,
    @catalog_id::uuid, @namespace::text[], @table_name::text,
    sqlc.narg(binding_id)::uuid, @definition::jsonb, @format_version::int,
    sqlc.narg(owner_user_id)::uuid, @updated_by::uuid
)
RETURNING *;

-- name: UpdateSemanticMetric :one
UPDATE semantic_metrics
SET slug = @slug::text,
    label = @label::text,
    description = @description::text,
    certified = @certified::boolean,
    catalog_id = @catalog_id::uuid,
    namespace = @namespace::text[],
    table_name = @table_name::text,
    binding_id = sqlc.narg(binding_id)::uuid,
    definition = @definition::jsonb,
    format_version = @format_version::int,
    owner_user_id = sqlc.narg(owner_user_id)::uuid,
    updated_by = @updated_by::uuid,
    updated_at = now()
WHERE workspace_id = @workspace_id::uuid AND id = @id::uuid
RETURNING *;

-- name: DeleteSemanticMetric :execrows
DELETE FROM semantic_metrics
WHERE workspace_id = @workspace_id::uuid AND id = @id::uuid;
