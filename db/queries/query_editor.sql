-- name: CreateQueryFolder :one
INSERT INTO query_folders (workspace_id, parent_id, name)
VALUES ($1, $2, $3)
RETURNING *;

-- name: ListQueryFolders :many
SELECT * FROM query_folders
WHERE workspace_id = $1 AND parent_id IS NOT DISTINCT FROM $2
ORDER BY name;

-- name: DeleteQueryFolder :exec
DELETE FROM query_folders WHERE id = $1 AND workspace_id = $2;

-- name: CreateSavedQuery :one
INSERT INTO saved_queries (workspace_id, folder_id, created_by, name, sql)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetSavedQueryByID :one
SELECT * FROM saved_queries WHERE id = $1 AND workspace_id = $2;

-- name: ListSavedQueries :many
SELECT * FROM saved_queries
WHERE workspace_id = $1 AND folder_id IS NOT DISTINCT FROM $2
ORDER BY name;

-- name: UpdateSavedQuery :one
UPDATE saved_queries
SET name = $3, sql = $4, folder_id = $5, updated_at = now()
WHERE id = $1 AND workspace_id = $2
RETURNING *;

-- name: DeleteSavedQuery :exec
DELETE FROM saved_queries WHERE id = $1 AND workspace_id = $2;

-- name: CreateQueryExecution :one
INSERT INTO query_executions (workspace_id, user_id, saved_query_id, status, sql, trino_catalog)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: UpdateQueryExecution :one
UPDATE query_executions
SET status = $2, trino_query_id = $3, rows_returned = $4, duration_ms = $5, finished_at = $6
WHERE id = $1
RETURNING *;

-- name: SetExecutionResult :exec
UPDATE query_executions
SET result_key = $2
WHERE id = $1;

-- name: GetQueryExecutionByID :one
SELECT * FROM query_executions WHERE id = $1;

-- name: ListQueryExecutionsByWorkspace :many
SELECT * FROM query_executions
WHERE workspace_id = $1
ORDER BY started_at DESC
LIMIT $2 OFFSET $3;

-- name: CreateQueryStatement :one
INSERT INTO query_statements (execution_id, position, sql, status)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: UpdateQueryStatement :one
UPDATE query_statements
SET status = $2, trino_query_id = $3, rows_returned = $4, duration_ms = $5, error_message = $6, finished_at = $7
WHERE id = $1
RETURNING *;

-- name: ListQueryStatements :many
SELECT * FROM query_statements
WHERE execution_id = $1
ORDER BY position;
