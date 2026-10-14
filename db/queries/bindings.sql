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

-- name: UpsertNativeCatalog :one
-- A catalog found inside an engine, owned by the engine's connection.
INSERT INTO catalogs (connection_id, name, origin, enabled)
VALUES ($1, $2, 'native', true)
ON CONFLICT (connection_id, name) DO UPDATE SET updated_at = now()
RETURNING *;

-- name: UpsertNativeBinding :one
INSERT INTO catalog_bindings (catalog_id, engine_id, engine_catalog_name, mode)
VALUES ($1, $2, $3, 'native')
ON CONFLICT (catalog_id, engine_id) DO UPDATE SET
    engine_catalog_name = EXCLUDED.engine_catalog_name,
    updated_at          = now()
RETURNING *;

-- name: DeleteNativeCatalogsOfEngine :exec
-- The native catalogs of an engine, deleted with it (their bindings cascade).
DELETE FROM catalogs c
USING catalog_bindings b
WHERE b.catalog_id = c.id AND b.engine_id = $1 AND b.mode = 'native' AND c.origin = 'native';

-- name: ListWorkspaceTargets :many
-- Every binding of a catalog the workspace may use, with the strongest
-- permission it holds on that catalog: 'write' for the catalogs of the owner
-- of a personal workspace (their own connections), else the grant (#16).
-- Disabled bindings, engines and catalogs are included; callers filter.
WITH visible AS (
    SELECT c.id AS catalog_id, 'write'::text AS permission
    FROM catalogs c
    JOIN catalog_connections cc ON cc.id = c.connection_id
    JOIN workspaces w ON w.id = sqlc.arg('workspace_id')
    WHERE w.type = 'personal' AND w.owner_user_id IS NOT NULL AND cc.created_by = w.owner_user_id
    UNION ALL
    SELECT g.catalog_id, g.permission
    FROM workspace_catalog_grants g
    WHERE g.workspace_id = sqlc.arg('workspace_id')
), perm AS (
    SELECT catalog_id,
           (CASE WHEN bool_or(permission = 'write') THEN 'write' ELSE 'read' END)::text AS permission
    FROM visible
    GROUP BY catalog_id
)
SELECT
    b.id                  AS binding_id,
    b.engine_catalog_name,
    b.mode,
    b.enabled             AS binding_enabled,
    e.id                  AS engine_id,
    e.name                AS engine_name,
    e.kind                AS engine_kind,
    e.enabled             AS engine_enabled,
    c.id                  AS catalog_id,
    c.name                AS catalog_name,
    c.enabled             AS catalog_enabled,
    p.permission
FROM perm p
JOIN catalogs c ON c.id = p.catalog_id
JOIN catalog_bindings b ON b.catalog_id = c.id
JOIN engines e ON e.id = b.engine_id
ORDER BY c.name, e.name;
