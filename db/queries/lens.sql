-- Monitoring snapshots (#117): every row is tagged with its engine or logical
-- catalog, so two engines never mix.

-- name: UpsertTrinoQuerySnapshot :one
INSERT INTO trino_query_snapshots (trino_query_id, engine_id, execution_id, origin, "user", state, sql, catalog, "schema", created_at, elapsed_ms, cpu_ms, physical_input_bytes, output_rows, error_message, raw_trino_payload)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
ON CONFLICT (trino_query_id) DO UPDATE
-- Two writers (the poller and the query events, #406). A poll read before the
-- query ended is stale once a terminal state is stored: it changes neither the
-- state nor the final figures, and only fills one still missing. Otherwise a
-- figure one writer does not know (the poller has no output rows) keeps the
-- other's.
SET state = CASE
        WHEN trino_query_snapshots.state IN ('finished', 'failed') AND EXCLUDED.state = 'running'
        THEN trino_query_snapshots.state
        ELSE EXCLUDED.state
    END,
    elapsed_ms = CASE
        WHEN trino_query_snapshots.state IN ('finished', 'failed') AND EXCLUDED.state = 'running'
        THEN COALESCE(trino_query_snapshots.elapsed_ms, EXCLUDED.elapsed_ms)
        ELSE COALESCE(EXCLUDED.elapsed_ms, trino_query_snapshots.elapsed_ms)
    END,
    cpu_ms = CASE
        WHEN trino_query_snapshots.state IN ('finished', 'failed') AND EXCLUDED.state = 'running'
        THEN COALESCE(trino_query_snapshots.cpu_ms, EXCLUDED.cpu_ms)
        ELSE COALESCE(EXCLUDED.cpu_ms, trino_query_snapshots.cpu_ms)
    END,
    physical_input_bytes = CASE
        WHEN trino_query_snapshots.state IN ('finished', 'failed') AND EXCLUDED.state = 'running'
        THEN COALESCE(trino_query_snapshots.physical_input_bytes, EXCLUDED.physical_input_bytes)
        ELSE COALESCE(EXCLUDED.physical_input_bytes, trino_query_snapshots.physical_input_bytes)
    END,
    output_rows = COALESCE(EXCLUDED.output_rows, trino_query_snapshots.output_rows),
    error_message = COALESCE(EXCLUDED.error_message, trino_query_snapshots.error_message),
    last_seen_at = now()
RETURNING *;

-- name: GetTrinoQuerySnapshot :one
SELECT * FROM trino_query_snapshots WHERE trino_query_id = $1;

-- name: ListTrinoQuerySnapshots :many
SELECT * FROM trino_query_snapshots
ORDER BY created_at DESC
LIMIT $1 OFFSET $2;

-- name: ListTrinoQuerySnapshotsByUser :many
SELECT * FROM trino_query_snapshots
WHERE "user" = $1
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;

-- name: ListActiveTrinoQueries :many
SELECT * FROM trino_query_snapshots
WHERE state = 'running'
ORDER BY created_at DESC;

-- name: ListTrinoQuerySnapshotsByEngine :many
SELECT * FROM trino_query_snapshots
WHERE engine_id = @engine_id::uuid
  AND (sqlc.narg(state)::text IS NULL OR state = sqlc.narg(state)::text)
  AND (sqlc.narg(user_name)::text IS NULL OR "user" = sqlc.narg(user_name)::text)
  AND (sqlc.narg(q)::text IS NULL OR sql ILIKE '%' || sqlc.narg(q)::text || '%')
ORDER BY created_at DESC
LIMIT @lim::int OFFSET @off::int;

-- name: GetTrinoQuerySnapshotByEngine :one
SELECT * FROM trino_query_snapshots
WHERE engine_id = @engine_id::uuid AND trino_query_id = @trino_query_id::text;

-- name: InsertClusterSnapshot :one
INSERT INTO cluster_snapshots (engine_id, active_workers, running_queries, queued_queries, blocked_queries, total_memory_bytes, free_memory_bytes, raw_payload)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: GetLatestClusterSnapshot :one
SELECT * FROM cluster_snapshots ORDER BY captured_at DESC LIMIT 1;

-- name: GetLatestClusterSnapshotByEngine :one
SELECT * FROM cluster_snapshots WHERE engine_id = $1 ORDER BY captured_at DESC LIMIT 1;

-- name: ListClusterSnapshots :many
SELECT * FROM cluster_snapshots
ORDER BY captured_at DESC
LIMIT $1 OFFSET $2;

-- name: ListClusterSnapshotsByEngine :many
SELECT * FROM cluster_snapshots
WHERE engine_id = @engine_id::uuid AND captured_at >= @from_time::timestamptz
ORDER BY captured_at ASC
LIMIT @lim::int;

-- name: UpsertTableHealthSnapshot :one
INSERT INTO table_health_snapshots (
    catalog_id, catalog_connection_id, catalog, namespace, table_name, snapshot_count, file_count,
    total_size_bytes, oldest_snapshot_age_hours, last_maintenance_at, health_score, needs_compaction,
    needs_snapshot_expiry, delete_file_count, equality_delete_count, position_delete_count,
    small_file_count, commits_last_hour, commits_last_day, avg_file_size_bytes, manifest_count,
    format_version, table_properties, due_operations, policy_revision)
VALUES (
    @catalog_id, @catalog_connection_id, @catalog, @namespace, @table_name, @snapshot_count, @file_count,
    @total_size_bytes, @oldest_snapshot_age_hours, @last_maintenance_at, @health_score, @needs_compaction,
    @needs_snapshot_expiry, sqlc.narg(delete_file_count)::bigint, sqlc.narg(equality_delete_count)::bigint,
    sqlc.narg(position_delete_count)::bigint, sqlc.narg(small_file_count)::bigint,
    sqlc.narg(commits_last_hour)::int, sqlc.narg(commits_last_day)::int, sqlc.narg(avg_file_size_bytes)::bigint,
    sqlc.narg(manifest_count)::int, sqlc.narg(format_version)::int, @table_properties::jsonb,
    @due_operations::text[], sqlc.narg(policy_revision)::bigint)
ON CONFLICT (catalog_id, namespace, table_name) DO UPDATE
SET captured_at               = now(),
    catalog_connection_id     = EXCLUDED.catalog_connection_id,
    catalog                   = EXCLUDED.catalog,
    snapshot_count            = EXCLUDED.snapshot_count,
    file_count                = EXCLUDED.file_count,
    total_size_bytes          = EXCLUDED.total_size_bytes,
    oldest_snapshot_age_hours = EXCLUDED.oldest_snapshot_age_hours,
    last_maintenance_at       = COALESCE(EXCLUDED.last_maintenance_at, table_health_snapshots.last_maintenance_at),
    health_score              = EXCLUDED.health_score,
    needs_compaction          = EXCLUDED.needs_compaction,
    needs_snapshot_expiry     = EXCLUDED.needs_snapshot_expiry,
    delete_file_count         = EXCLUDED.delete_file_count,
    equality_delete_count     = EXCLUDED.equality_delete_count,
    position_delete_count     = EXCLUDED.position_delete_count,
    small_file_count          = EXCLUDED.small_file_count,
    commits_last_hour         = EXCLUDED.commits_last_hour,
    commits_last_day          = EXCLUDED.commits_last_day,
    avg_file_size_bytes       = EXCLUDED.avg_file_size_bytes,
    manifest_count            = EXCLUDED.manifest_count,
    format_version            = EXCLUDED.format_version,
    table_properties          = EXCLUDED.table_properties,
    due_operations            = EXCLUDED.due_operations,
    policy_revision           = EXCLUDED.policy_revision
RETURNING *;

-- name: ListTableHealthSnapshots :many
SELECT * FROM table_health_snapshots
ORDER BY health_score ASC
LIMIT $1 OFFSET $2;

-- name: ListTableHealthByConnection :many
SELECT * FROM table_health_snapshots
WHERE catalog_connection_id = $1
ORDER BY health_score ASC
LIMIT $2 OFFSET $3;

-- name: ListTableHealthByCatalog :many
SELECT * FROM table_health_snapshots
WHERE catalog_id = @catalog_id::uuid
  AND (sqlc.narg(ns)::text IS NULL OR namespace = sqlc.narg(ns)::text)
  AND (sqlc.narg(needs)::text IS NULL
       OR (sqlc.narg(needs)::text = 'compaction' AND needs_compaction)
       OR (sqlc.narg(needs)::text = 'snapshot_expiry' AND needs_snapshot_expiry)
       OR sqlc.narg(needs)::text = ANY (due_operations))
  AND (sqlc.narg(max_score)::int IS NULL OR health_score <= sqlc.narg(max_score)::int)
ORDER BY health_score ASC, namespace, table_name
LIMIT @lim::int OFFSET @off::int;

-- name: GetTableHealthSnapshot :one
SELECT * FROM table_health_snapshots
WHERE catalog_connection_id = $1 AND catalog = $2 AND namespace = $3 AND table_name = $4;

-- name: CreateMaintenanceRun :one
INSERT INTO maintenance_runs (catalog_id, binding_id, namespace, table_name, operations, retention, status, started_by, workflow_id)
VALUES (@catalog_id::uuid, sqlc.narg(binding_id)::uuid, @namespace::text, @table_name::text,
        @operations::text[], sqlc.narg(retention)::text, 'running', sqlc.narg(started_by)::uuid, @workflow_id::text)
RETURNING *;

-- name: UpdateMaintenanceRun :one
UPDATE maintenance_runs
SET status = @status::text, error_code = sqlc.narg(error_code)::text,
    error_message = sqlc.narg(error_message)::text, finished_at = now()
WHERE id = @id::uuid
RETURNING *;

-- name: ListMaintenanceRuns :many
SELECT * FROM maintenance_runs
WHERE catalog_id = @catalog_id::uuid
  AND (sqlc.narg(trigger)::text IS NULL OR trigger = sqlc.narg(trigger)::text)
  AND (sqlc.narg(operation)::text IS NULL OR sqlc.narg(operation)::text = ANY (operations))
  AND (sqlc.narg(status)::text IS NULL OR status = sqlc.narg(status)::text)
  AND (sqlc.narg(ns)::text IS NULL OR namespace = sqlc.narg(ns)::text)
  AND (sqlc.narg(tbl)::text IS NULL OR table_name = sqlc.narg(tbl)::text)
ORDER BY started_at DESC
LIMIT @lim::int OFFSET @off::int;

-- name: GetMaintenanceRun :one
SELECT * FROM maintenance_runs WHERE id = @id::uuid;
