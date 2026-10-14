-- name: CreateEngine :one
INSERT INTO engines (name, kind, class, connection_id, identity_mode, created_by)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetEngineByID :one
SELECT * FROM engines WHERE id = $1;

-- name: ListEngines :many
SELECT * FROM engines ORDER BY name;

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
