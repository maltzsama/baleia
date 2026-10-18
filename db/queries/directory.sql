-- name: CreateDirectoryReconcileRun :one
INSERT INTO directory_reconcile_runs (provider_id)
VALUES ($1)
RETURNING *;

-- name: FinishDirectoryReconcileRun :exec
UPDATE directory_reconcile_runs
SET finished_at = now(),
    outcome = $2,
    seen = $3,
    suspended = $4,
    reinstated = $5,
    error = $6,
    actions = $7
WHERE id = $1;

-- name: ListDirectoryReconcileRuns :many
-- provider_id NULL lists every provider; newest first.
SELECT * FROM directory_reconcile_runs
WHERE ($1::uuid IS NULL OR provider_id = $1)
ORDER BY started_at DESC
LIMIT $2;
