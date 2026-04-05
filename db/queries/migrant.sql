-- name: CreateMigration :one
INSERT INTO migrations (
    created_by, name, source_type, source_url,
    source_credentials_enc, target_catalog_connection_id
) VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetMigrationByID :one
SELECT * FROM migrations WHERE id = $1;

-- name: ListMigrations :many
SELECT * FROM migrations ORDER BY created_at DESC;

-- name: UpdateMigrationStatus :exec
UPDATE migrations
SET status = $2, temporal_workflow_id = $3,
    started_at = CASE WHEN $2 = 'running' THEN now() ELSE started_at END,
    finished_at = CASE WHEN $2 IN ('finished','failed','cancelled') THEN now() ELSE finished_at END
WHERE id = $1;

-- name: UpdateMigrationProgress :exec
UPDATE migrations
SET migrated_tables = $2, failed_tables = $3
WHERE id = $1;

-- name: CreateMigrationItem :one
INSERT INTO migration_items (
    migration_id, source_namespace, source_table,
    target_namespace, target_table, status
) VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: UpdateMigrationItem :exec
UPDATE migration_items
SET status = $2, error_message = $3,
    started_at = CASE WHEN $2 = 'running' THEN now() ELSE started_at END,
    finished_at = CASE WHEN $2 IN ('finished','failed','skipped') THEN now() ELSE finished_at END
WHERE id = $1;

-- name: ListMigrationItems :many
SELECT * FROM migration_items
WHERE migration_id = $1
ORDER BY source_namespace, source_table;

-- name: ListPendingMigrationItems :many
SELECT * FROM migration_items
WHERE migration_id = $1 AND status = 'pending'
ORDER BY source_namespace, source_table;
