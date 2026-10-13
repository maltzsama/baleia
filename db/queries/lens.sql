-- Trino Query Snapshots

-- name: UpsertTrinoQuerySnapshot :one
INSERT INTO trino_query_snapshots (trino_query_id, execution_id, origin, "user", state, sql, catalog, "schema", created_at, elapsed_ms, cpu_ms, physical_input_bytes, output_rows, error_message, raw_trino_payload)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
ON CONFLICT (trino_query_id) DO UPDATE
SET state = EXCLUDED.state,
    elapsed_ms = EXCLUDED.elapsed_ms,
    cpu_ms = EXCLUDED.cpu_ms,
    physical_input_bytes = EXCLUDED.physical_input_bytes,
    output_rows = EXCLUDED.output_rows,
    error_message = EXCLUDED.error_message,
    last_seen_at = now()
RETURNING *;

-- name: GetTrinoQuerySnapshot :one
SELECT * FROM trino_query_snapshots WHERE trino_query_id = $1;

-- name: ListTrinoQuerySnapshots :many
SELECT * FROM trino_query_snapshots
ORDER BY created_at DESC
LIMIT $1 OFFSET $2;

-- name: ListTrinoQuerySnapshotsByUser :many
-- Lens shows non-admins only the queries run under their own Trino identity.
SELECT * FROM trino_query_snapshots
WHERE "user" = $1
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;

-- name: ListActiveTrinoQueries :many
SELECT * FROM trino_query_snapshots
WHERE state = 'running'
ORDER BY created_at DESC;

-- Cluster Snapshots

-- name: InsertClusterSnapshot :one
INSERT INTO cluster_snapshots (active_workers, running_queries, queued_queries, blocked_queries, total_memory_bytes, free_memory_bytes, raw_payload)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: GetLatestClusterSnapshot :one
SELECT * FROM cluster_snapshots ORDER BY captured_at DESC LIMIT 1;

-- name: ListClusterSnapshots :many
SELECT * FROM cluster_snapshots
ORDER BY captured_at DESC
LIMIT $1 OFFSET $2;

-- Table Health Snapshots

-- name: UpsertTableHealthSnapshot :one
INSERT INTO table_health_snapshots (catalog_connection_id, catalog, namespace, table_name, snapshot_count, file_count, total_size_bytes, oldest_snapshot_age_hours, last_maintenance_at, health_score, needs_compaction, needs_snapshot_expiry, needs_orphan_cleanup)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
ON CONFLICT (catalog_connection_id, catalog, namespace, table_name) DO UPDATE
SET captured_at               = now(),
    snapshot_count            = EXCLUDED.snapshot_count,
    file_count                = EXCLUDED.file_count,
    total_size_bytes          = EXCLUDED.total_size_bytes,
    oldest_snapshot_age_hours = EXCLUDED.oldest_snapshot_age_hours,
    last_maintenance_at       = COALESCE(EXCLUDED.last_maintenance_at, table_health_snapshots.last_maintenance_at),
    health_score              = EXCLUDED.health_score,
    needs_compaction          = EXCLUDED.needs_compaction,
    needs_snapshot_expiry     = EXCLUDED.needs_snapshot_expiry,
    needs_orphan_cleanup      = EXCLUDED.needs_orphan_cleanup
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

-- name: GetTableHealthSnapshot :one
SELECT * FROM table_health_snapshots
WHERE catalog_connection_id = $1 AND catalog = $2 AND namespace = $3 AND table_name = $4;
