-- name: ListBindingsByCatalog :many
SELECT * FROM catalog_bindings WHERE catalog_id = $1 ORDER BY created_at;

-- name: ListBindingsByEngine :many
SELECT * FROM catalog_bindings WHERE engine_id = $1 ORDER BY engine_catalog_name;

-- name: GetBindingForTarget :one
-- Everything an execution target needs about a binding: its engine and its
-- logical catalog.
SELECT
    b.id          AS binding_id,
    b.engine_catalog_name,
    b.enabled     AS binding_enabled,
    b.mode,
    e.id          AS engine_id,
    e.name        AS engine_name,
    e.kind        AS engine_kind,
    e.enabled     AS engine_enabled,
    c.id          AS catalog_id,
    c.name        AS catalog_name,
    c.enabled     AS catalog_enabled
FROM catalog_bindings b
JOIN engines e ON e.id = b.engine_id
JOIN catalogs c ON c.id = b.catalog_id
WHERE b.id = $1;

-- name: SetWorkspaceDefaultTarget :exec
UPDATE workspaces
SET default_binding_id = $2, default_schema = $3, updated_at = now()
WHERE id = $1;

-- name: SetExecutionTarget :exec
-- Stores the resolved target of an execution. target_snapshot is immutable
-- context for history and carries no secret.
UPDATE query_executions
SET engine_id         = $2,
    target_binding_id = $3,
    target_catalog_id = $4,
    target_schema     = $5,
    target_snapshot   = $6
WHERE id = $1;
