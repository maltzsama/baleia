-- name: CreateEngine :one
INSERT INTO engines (name, kind, class, connection_id, identity_mode, created_by)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetEngineByID :one
SELECT * FROM engines WHERE id = $1;

-- name: ListEngines :many
SELECT * FROM engines ORDER BY name;

-- name: ListEnabledEnginesWithConnection :many
-- Every enabled engine with its connection and, for a Trino engine, its
-- cluster: the metadata dispatcher refreshes their catalogs (#17).
SELECT sqlc.embed(e), sqlc.embed(c), tc.id AS cluster_id
FROM engines e
JOIN catalog_connections c ON c.id = e.connection_id
LEFT JOIN trino_clusters tc ON tc.engine_id = e.id
WHERE e.enabled
ORDER BY e.name;

-- name: GetEngineWithConnection :one
-- The engine and the connection it runs on (decrypted by the caller, #19).
SELECT sqlc.embed(e), sqlc.embed(c)
FROM engines e
JOIN catalog_connections c ON c.id = e.connection_id
WHERE e.id = $1;

-- name: GetDefaultTrinoEngine :one
-- The engine linked to the 'default' Trino cluster, with its connection.
SELECT sqlc.embed(e), sqlc.embed(c)
FROM engines e
JOIN trino_clusters tc ON tc.engine_id = e.id
JOIN catalog_connections c ON c.id = e.connection_id
WHERE tc.name = 'default';

-- name: LinkTrinoClusterEngine :exec
UPDATE trino_clusters SET engine_id = $2, updated_at = now() WHERE id = $1;

-- name: UpdateEngine :one
UPDATE engines
SET enabled       = COALESCE(sqlc.narg('enabled'), enabled),
    identity_mode = COALESCE(sqlc.narg('identity_mode'), identity_mode),
    updated_at    = now()
WHERE id = sqlc.arg('id')
RETURNING *;

-- name: DeleteEngine :exec
DELETE FROM engines WHERE id = $1;

-- name: CountEngineBlockers :one
-- What stops an engine from being deleted: managed bindings and executions
-- still running on it. Native bindings go with the engine.
SELECT
    (SELECT count(*) FROM catalog_bindings b WHERE b.engine_id = $1 AND b.mode = 'managed')::bigint AS managed_bindings,
    (SELECT count(*) FROM query_executions q WHERE q.engine_id = $1 AND q.status = 'running')::bigint AS running_executions;

-- name: EngineHostsCatalogGrantedToUser :one
-- True when the engine has a binding of a catalog granted to a workspace the
-- user belongs to.
SELECT EXISTS (
    SELECT 1 FROM catalog_bindings b
    JOIN workspace_catalog_grants g ON g.catalog_id = b.catalog_id
    JOIN workspaces w ON w.id = g.workspace_id
    LEFT JOIN team_members tm ON tm.team_id = w.owner_team_id AND tm.user_id = sqlc.arg('user_id')
    WHERE b.engine_id = sqlc.arg('engine_id')
      AND (w.owner_user_id = sqlc.arg('user_id') OR tm.user_id = sqlc.arg('user_id'))
) AS granted;

-- name: EngineHostsCatalogGrantedToAgent :one
-- True when the engine hosts a catalog granted to a workspace the agent is
-- bound to and the owner is still a member of that workspace (#59).
SELECT EXISTS (
    SELECT 1 FROM catalog_bindings b
    JOIN workspace_catalog_grants g ON g.catalog_id = b.catalog_id
    JOIN workspaces w ON w.id = g.workspace_id
    JOIN agent_workspaces aw ON aw.workspace_id = g.workspace_id AND aw.agent_id = sqlc.arg('agent_id')
    LEFT JOIN team_members tm ON tm.team_id = w.owner_team_id AND tm.user_id = sqlc.arg('owner_id')
    WHERE b.engine_id = sqlc.arg('engine_id')
      AND (w.owner_user_id = sqlc.arg('owner_id') OR tm.user_id = sqlc.arg('owner_id'))
) AS granted;

-- name: ListEnginesVisibleToUser :many
-- The engines a non-admin may see: those on connections they own and those
-- hosting a catalog granted to one of their workspaces.
SELECT e.* FROM engines e
JOIN catalog_connections c ON c.id = e.connection_id
WHERE c.created_by = sqlc.arg('user_id')
   OR EXISTS (
        SELECT 1 FROM catalog_bindings b
        JOIN workspace_catalog_grants g ON g.catalog_id = b.catalog_id
        JOIN workspaces w ON w.id = g.workspace_id
        LEFT JOIN team_members tm ON tm.team_id = w.owner_team_id AND tm.user_id = sqlc.arg('user_id')
        WHERE b.engine_id = e.id
          AND (w.owner_user_id = sqlc.arg('user_id') OR tm.user_id = sqlc.arg('user_id')))
ORDER BY e.name;

-- name: ListEngineObservations :many
-- Catalogs the engine reported, with the managed/adopted mode (#38).
SELECT * FROM engine_catalog_observations
WHERE engine_id = $1
ORDER BY catalog_name;

-- name: CountEngineImpact :one
-- What depends on an engine before disabling or deleting it (#110).
SELECT
    (SELECT count(*) FROM catalog_bindings b WHERE b.engine_id = $1 AND b.mode = 'managed')::bigint AS managed_bindings,
    (SELECT count(*) FROM catalog_bindings b JOIN catalogs c ON c.id = b.catalog_id
      WHERE b.engine_id = $1 AND b.mode = 'native' AND c.origin = 'native')::bigint AS native_catalogs,
    (SELECT count(*) FROM workspace_catalog_grants g
      JOIN catalog_bindings b ON b.catalog_id = g.catalog_id WHERE b.engine_id = $1)::bigint AS grants,
    (SELECT count(*) FROM semantic_metrics m
      JOIN catalog_bindings b ON (b.catalog_id = m.catalog_id OR b.id = m.binding_id)
      WHERE b.engine_id = $1)::bigint AS metrics,
    0::bigint AS docs,
    (SELECT count(*) FROM saved_queries s
      JOIN catalog_bindings b ON b.id = s.target_binding_id WHERE b.engine_id = $1)::bigint AS saved_queries_targeting,
    (SELECT count(*) FROM query_executions e WHERE e.engine_id = $1 AND e.status = 'running')::bigint AS running_executions;
