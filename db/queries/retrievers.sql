-- name: CreateRetriever :one
INSERT INTO retrievers (
    workspace_id, created_by, name,
    definition_sql, dest_catalog, dest_namespace, dest_table,
    cron_schedule, refresh_type
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING *;

-- name: GetRetrieverByID :one
SELECT * FROM retrievers WHERE id = $1 AND workspace_id = $2;

-- name: ListRetrieversByWorkspace :many
SELECT * FROM retrievers
WHERE workspace_id = $1
ORDER BY name;

-- name: ListRetrieversDue :many
SELECT * FROM retrievers
WHERE status = 'active' AND next_refresh_at <= now()
ORDER BY next_refresh_at;

-- name: UpdateRetriever :one
UPDATE retrievers
SET name = $3, definition_sql = $4, cron_schedule = $5,
    refresh_type = $6, status = $7, updated_at = now()
WHERE id = $1 AND workspace_id = $2
RETURNING *;

-- name: UpdateRetrieverAfterRefresh :exec
UPDATE retrievers
SET status = $2, last_refresh_at = now(), next_refresh_at = $3, updated_at = now()
WHERE id = $1;

-- name: CreateRetrieverRefreshHistory :one
INSERT INTO retriever_refresh_history (retriever_id, temporal_workflow_id, refresh_type, status)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: UpdateRetrieverRefreshHistory :exec
UPDATE retriever_refresh_history
SET status = $2, rows_processed = $3, duration_ms = $4, error_message = $5, finished_at = now()
WHERE id = $1;

-- name: ListRetrieverRefreshHistory :many
SELECT * FROM retriever_refresh_history
WHERE retriever_id = $1
ORDER BY started_at DESC
LIMIT $2;
