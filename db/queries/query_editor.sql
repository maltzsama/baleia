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
-- The target columns are the resolved execution target (#13); trino_catalog
-- keeps the engine catalog for the Trino workflow until it reads the target.
INSERT INTO query_executions (
    workspace_id, user_id, saved_query_id, status, sql, trino_catalog, request_id,
    engine_id, target_binding_id, target_catalog_id, target_schema, target_snapshot)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
RETURNING *;

-- name: UpdateQueryExecution :one
-- Only a running execution transitions, so a late completion or failure can
-- never overwrite a cancellation. Zero matched rows (pgx.ErrNoRows) means the
-- execution was already terminal; callers treat that as a no-op.
UPDATE query_executions
SET status = $2, trino_query_id = $3, rows_returned = $4, duration_ms = $5, finished_at = $6
WHERE id = $1 AND status = 'running'
RETURNING *;

-- name: CancelQueryExecution :one
-- Conditional cancel: only a running execution transitions to cancelled, so a
-- concurrent completion or failure activity cannot be overwritten. Zero matched
-- rows (pgx.ErrNoRows) means the execution is gone, foreign, or already terminal.
UPDATE query_executions
SET status = 'cancelled', finished_at = now()
WHERE id = $1 AND workspace_id = $2 AND status = 'running'
RETURNING *;

-- name: SetExecutionResult :exec
UPDATE query_executions
SET result_key = $2
WHERE id = $1;

-- name: GetQueryExecutionByID :one
SELECT * FROM query_executions WHERE id = $1;

-- name: GetQueryExecutionByIDAndWorkspace :one
-- Workspace-scoped lookup: an execution id from another workspace must not be
-- readable. Callers use this instead of GetQueryExecutionByID for user-facing
-- reads so a cross-workspace id yields ErrNoRows (mapped to 404).
SELECT * FROM query_executions WHERE id = $1 AND workspace_id = $2;

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
-- Conditional update: only a running statement transitions, so a late
-- completion can never overwrite a cancellation. Zero matched rows means the
-- statement was already terminal; callers treat that as a no-op.
UPDATE query_statements
SET status = $2, trino_query_id = $3, rows_returned = $4, duration_ms = $5, error_message = $6, finished_at = $7,
    result_key = $8, result_bytes = $9, truncated = $10
WHERE id = $1 AND status = 'running'
RETURNING *;

-- name: ListQueryStatements :many
SELECT * FROM query_statements
WHERE execution_id = $1
ORDER BY position;

-- name: ListQueryStatementsByWorkspace :many
-- Only returns statements of an execution that belongs to the given workspace.
SELECT s.* FROM query_statements s
JOIN query_executions e ON e.id = s.execution_id
WHERE s.execution_id = $1 AND e.workspace_id = $2
ORDER BY s.position;

-- name: GetQueryStatementByPosition :one
-- Single statement of an execution that belongs to the given workspace.
SELECT s.* FROM query_statements s
JOIN query_executions e ON e.id = s.execution_id
WHERE s.execution_id = $1 AND e.workspace_id = $2 AND s.position = $3;
