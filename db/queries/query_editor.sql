-- name: CreateQueryFolder :one
INSERT INTO query_folders (workspace_id, space_id, parent_id, name, slug)
VALUES (@workspace_id::uuid, @space_id::uuid, sqlc.narg(parent_id)::uuid, @name::text, @slug::text)
RETURNING *;

-- name: ListVisibleQueryFolders :many
-- Only folders of spaces the caller can see; #16 says to filter in SQL, not in
-- a Go loop. space_id/folder_id narrow the listing when present.
SELECT f.* FROM query_folders f
WHERE f.workspace_id = @workspace_id::uuid
  AND baleia_effective_space_role(f.workspace_id, f.space_id, @user_id::uuid) IS NOT NULL
  AND (sqlc.narg(space_id)::uuid IS NULL OR f.space_id = sqlc.narg(space_id)::uuid)
  AND f.parent_id IS NOT DISTINCT FROM sqlc.narg(parent_id)::uuid
ORDER BY f.name;

-- name: GetQueryFolderByID :one
SELECT * FROM query_folders WHERE id = @id::uuid AND workspace_id = @workspace_id::uuid;

-- name: DeleteQueryFolder :execrows
DELETE FROM query_folders WHERE id = @id::uuid AND workspace_id = @workspace_id::uuid;

-- name: FolderSlugExists :one
SELECT EXISTS (
    SELECT 1 FROM query_folders WHERE space_id = @space_id::uuid AND slug = @slug::text
) AS exists;

-- name: CreateSavedQuery :one
INSERT INTO saved_queries (
    workspace_id, space_id, folder_id, created_by, updated_by, name, slug, sql,
    target_binding_id, target_schema
)
VALUES (
    @workspace_id::uuid, @space_id::uuid, sqlc.narg(folder_id)::uuid, @created_by::uuid,
    sqlc.narg(updated_by)::uuid, @name::text, @slug::text, @sql::text,
    sqlc.narg(target_binding_id)::uuid, sqlc.narg(target_schema)::text
)
RETURNING *;

-- name: GetSavedQueryByID :one
SELECT * FROM saved_queries WHERE id = @id::uuid AND workspace_id = @workspace_id::uuid;

-- name: ListVisibleSavedQueries :many
-- Only queries of spaces the caller can see; space_id/folder_id/q narrow it.
SELECT q.* FROM saved_queries q
WHERE q.workspace_id = @workspace_id::uuid
  AND baleia_effective_space_role(q.workspace_id, q.space_id, @user_id::uuid) IS NOT NULL
  AND (sqlc.narg(space_id)::uuid IS NULL OR q.space_id = sqlc.narg(space_id)::uuid)
  AND q.folder_id IS NOT DISTINCT FROM sqlc.narg(folder_id)::uuid
  AND (sqlc.narg(q)::text IS NULL OR q.name ILIKE '%' || sqlc.narg(q)::text || '%' OR q.slug ILIKE '%' || sqlc.narg(q)::text || '%')
ORDER BY q.name;

-- name: ListSavedQueryVersions :many
-- Latest content version of every saved query in the workspace, in one query.
SELECT entity_id, max(version)::int AS version
FROM content_versions
WHERE workspace_id = @workspace_id::uuid AND entity_type = 'saved_query'
GROUP BY entity_id;

-- name: SavedQuerySlugExists :one
SELECT EXISTS (
    SELECT 1 FROM saved_queries WHERE workspace_id = @workspace_id::uuid AND slug = @slug::text
) AS exists;

-- name: UpdateSavedQuery :one
UPDATE saved_queries
SET name = @name::text,
    sql = @sql::text,
    folder_id = sqlc.narg(folder_id)::uuid,
    space_id = @space_id::uuid,
    slug = @slug::text,
    target_binding_id = sqlc.narg(target_binding_id)::uuid,
    target_schema = sqlc.narg(target_schema)::text,
    updated_by = sqlc.narg(updated_by)::uuid,
    updated_at = now()
WHERE id = @id::uuid AND workspace_id = @workspace_id::uuid
RETURNING *;

-- name: DeleteSavedQuery :execrows
DELETE FROM saved_queries WHERE id = @id::uuid AND workspace_id = @workspace_id::uuid;

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
SET status = $2, engine_query_id = $3, rows_returned = $4, duration_ms = $5, finished_at = $6
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

-- name: ListExecutionHistory :many
-- Newest first, keyset-paginated on (started_at, id): the cursor is the last
-- row of the previous page. Every filter is optional; the handler narrows
-- user_id to the caller unless the caller owns the workspace or is an admin.
SELECT qe.id, qe.workspace_id, qe.user_id, u.name AS user_name,
       qe.saved_query_id, qe.status, qe.sql, qe.engine_query_id,
       qe.rows_returned, qe.duration_ms, qe.started_at, qe.finished_at,
       qe.trino_catalog, qe.result_key, qe.request_id, qe.engine_id,
       qe.target_binding_id, qe.target_catalog_id, qe.target_schema,
       qe.target_snapshot
FROM query_executions qe
JOIN users u ON u.id = qe.user_id
WHERE qe.workspace_id = @workspace_id::uuid
  AND (sqlc.narg(user_id)::uuid IS NULL OR qe.user_id = sqlc.narg(user_id)::uuid)
  AND (sqlc.narg(status)::text IS NULL OR qe.status = sqlc.narg(status)::text)
  AND (sqlc.narg(from_time)::timestamptz IS NULL OR qe.started_at >= sqlc.narg(from_time)::timestamptz)
  AND (sqlc.narg(to_time)::timestamptz IS NULL OR qe.started_at <= sqlc.narg(to_time)::timestamptz)
  AND (sqlc.narg(q)::text IS NULL OR qe.sql ILIKE '%' || sqlc.narg(q)::text || '%')
  AND (sqlc.narg(catalog_id)::uuid IS NULL OR qe.target_catalog_id = sqlc.narg(catalog_id)::uuid)
  AND (sqlc.narg(engine_id)::uuid IS NULL OR qe.engine_id = sqlc.narg(engine_id)::uuid)
  AND (sqlc.narg(cursor_at)::timestamptz IS NULL
       OR (qe.started_at, qe.id) < (sqlc.narg(cursor_at)::timestamptz, sqlc.narg(cursor_id)::uuid))
ORDER BY qe.started_at DESC, qe.id DESC
LIMIT @lim;

-- name: CreateQueryStatement :one
INSERT INTO query_statements (execution_id, position, sql, status)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: UpdateQueryStatement :one
-- Conditional update: only a running statement transitions, so a late
-- completion can never overwrite a cancellation. Zero matched rows means the
-- statement was already terminal; callers treat that as a no-op.
UPDATE query_statements
SET status = $2, engine_query_id = $3, rows_returned = $4, duration_ms = $5, error_message = $6, finished_at = $7,
    result_key = $8, result_bytes = $9, truncated = $10, result_sets = $11, error_code = $12
WHERE id = $1 AND status = 'running'
RETURNING *;

-- name: StartQueryStatement :execrows
-- A pending statement becomes the running one. Zero rows means the
-- statement is no longer pending (already abandoned).
UPDATE query_statements
SET status = 'running', started_at = now()
WHERE execution_id = $1 AND position = $2 AND status = 'pending';

-- name: SkipPendingStatements :exec
-- After a failure, a cancel or a crash the statements that never ran are
-- skipped.
UPDATE query_statements
SET status = 'skipped', finished_at = now()
WHERE execution_id = $1 AND status = 'pending';

-- name: AbandonRunningStatements :many
-- Ends the statements still running after the script activity stopped
-- (cancel, crash, heartbeat timeout) and returns their handles so the engine
-- can be asked to stop them.
UPDATE query_statements
SET status = $2, error_code = $3, error_message = $4, finished_at = now()
WHERE execution_id = $1 AND status = 'running'
RETURNING position, query_handle;

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

-- name: SetStatementQueryHandle :exec
-- Persists the engine's handle of a running statement as soon as it is known,
-- so the API can cancel it from another process.
UPDATE query_statements
SET query_handle = $3
WHERE execution_id = $1 AND position = $2 AND status = 'running';

-- name: ListRunningStatementHandles :many
SELECT position, query_handle
FROM query_statements
WHERE execution_id = $1 AND query_handle IS NOT NULL AND status = 'running'
ORDER BY position;
