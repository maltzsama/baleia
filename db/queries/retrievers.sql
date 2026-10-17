-- name: CreateRetriever :one
INSERT INTO retrievers (
    workspace_id, created_by, name,
    definition_sql, dest_catalog, dest_namespace, dest_table, dest_binding_id,
    cron_schedule, refresh_type, next_refresh_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
RETURNING *;

-- name: ListRetrieverDestinations :many
-- Bindings a workspace may write to: write grant, Iceberg connector, active
-- binding on an enabled federated engine (#118).
SELECT b.id AS binding_id,
       b.engine_catalog_name,
       COALESCE(b.connector, '') AS connector,
       c.id AS catalog_id,
       c.name AS catalog_name,
       e.id AS engine_id,
       e.name AS engine_name,
       e.kind AS engine_kind
FROM catalog_bindings b
JOIN catalogs c ON c.id = b.catalog_id
JOIN engines e ON e.id = b.engine_id
JOIN workspace_catalog_grants g ON g.catalog_id = c.id AND g.workspace_id = @workspace_id::uuid
WHERE g.permission = 'write'
  AND b.connector = 'iceberg'
  AND b.lifecycle_state = 'active'
  AND b.enabled
  AND e.enabled
  AND e.class = 'federated'
ORDER BY c.name, b.engine_catalog_name;

-- name: LatestRetrieverRefresh :one
SELECT status, error_code FROM retriever_refresh_history
WHERE retriever_id = $1
ORDER BY started_at DESC
LIMIT 1;

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
SET name = $3, definition_sql = $4, dest_catalog = $5, dest_namespace = $6,
    dest_table = $7, dest_binding_id = $8, cron_schedule = $9, refresh_type = $10,
    status = $11, error_code = $12, next_refresh_at = $13, updated_at = now()
WHERE id = $1 AND workspace_id = $2
RETURNING *;

-- name: UpdateRetrieverAfterRefresh :exec
-- Fired by the refresh activity when a run finishes: status flips to active or
-- error, last_refresh_at advances. next_refresh_at is advanced by the
-- dispatcher when the run is *started* (AdvanceRetrieverSchedule), never here.
UPDATE retrievers
SET status = $2, error_code = $3, last_refresh_at = now(), updated_at = now()
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
SET status = $2, rows_processed = $3, duration_ms = $4, error_message = $5, error_code = $6, finished_at = now()
WHERE id = $1;

-- name: ListRetrieverRefreshHistory :many
SELECT * FROM retriever_refresh_history
WHERE retriever_id = $1
ORDER BY started_at DESC
LIMIT $2;

-- name: DeleteRetriever :execrows
-- Its refresh history goes with it (ON DELETE CASCADE).
DELETE FROM retrievers WHERE id = $1 AND workspace_id = $2;
