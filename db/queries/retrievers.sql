-- name: CreateRetriever :one
INSERT INTO retrievers (
    workspace_id, created_by, name,
    definition_sql, dest_catalog, dest_namespace, dest_table,
    cron_schedule, refresh_type, next_refresh_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
RETURNING *;

-- name: GetRetrieverByID :one
SELECT * FROM retrievers WHERE id = $1 AND workspace_id = $2;

-- name: GetRetrieverForRefresh :one
-- Worker-only lookup: the refresh activity runs off a due list, so it does not
-- have (and must not trust) a caller-supplied workspace id.
SELECT * FROM retrievers WHERE id = $1;

-- name: ListRetrieversByWorkspace :many
SELECT * FROM retrievers
WHERE workspace_id = $1
ORDER BY name;

-- name: ListRetrieversDue :many
SELECT * FROM retrievers
WHERE status = 'active' AND next_refresh_at <= now()
ORDER BY next_refresh_at
LIMIT 50;

-- name: UpdateRetriever :one
UPDATE retrievers
SET name = $3, definition_sql = $4, cron_schedule = $5,
    refresh_type = $6, status = $7, next_refresh_at = $8, updated_at = now()
WHERE id = $1 AND workspace_id = $2
RETURNING *;

-- name: UpdateRetrieverAfterRefresh :exec
-- Fired by the refresh activity when a run finishes: status flips to active or
-- error, last_refresh_at advances. next_refresh_at is advanced by the
-- dispatcher when the run is *started* (AdvanceRetrieverSchedule), never here.
UPDATE retrievers
SET status = $2, last_refresh_at = now(), updated_at = now()
WHERE id = $1;

-- name: AdvanceRetrieverSchedule :exec
-- Fired by the dispatcher when a refresh run is started: schedule the next run
-- from the cron expression immediately, so a long refresh never triggers a
-- burst of runs afterwards.
UPDATE retrievers
SET next_refresh_at = $2, updated_at = now()
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
