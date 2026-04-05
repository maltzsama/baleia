-- name: CreateLens :one
INSERT INTO lenses (
    catalog_connection_id, created_by, name,
    iceberg_catalog, iceberg_namespace, iceberg_table,
    cron_schedule,
    expire_snapshots, remove_orphan_files,
    compact_data_files, rewrite_manifests, remove_old_metadata
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
RETURNING *;

-- name: GetLensByID :one
SELECT * FROM lenses WHERE id = $1;

-- name: ListLenses :many
SELECT * FROM lenses ORDER BY name;

-- name: ListLensesDue :many
SELECT * FROM lenses
WHERE status = 'active' AND next_run_at <= now()
ORDER BY next_run_at;

-- name: UpdateLens :one
UPDATE lenses
SET name = $2, cron_schedule = $3,
    expire_snapshots = $4, remove_orphan_files = $5,
    compact_data_files = $6, rewrite_manifests = $7,
    remove_old_metadata = $8, status = $9,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: UpdateLensAfterRun :exec
UPDATE lenses
SET status = $2, last_run_at = now(), next_run_at = $3, updated_at = now()
WHERE id = $1;

-- name: CreateLensRunHistory :one
INSERT INTO lens_run_history (lens_id, temporal_workflow_id, status)
VALUES ($1, $2, $3)
RETURNING *;

-- name: UpdateLensRunHistory :exec
UPDATE lens_run_history
SET status = $2, operations_summary = $3, error_message = $4, duration_ms = $5, finished_at = now()
WHERE id = $1;

-- name: ListLensRunHistory :many
SELECT * FROM lens_run_history
WHERE lens_id = $1
ORDER BY started_at DESC
LIMIT $2;
