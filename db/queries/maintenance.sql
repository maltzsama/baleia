-- Per-table maintenance parameters and the runs they plan (#500).

-- name: GetTableMaintenancePolicy :one
SELECT * FROM table_maintenance_policies
WHERE catalog_id = @catalog_id::uuid AND namespace = @namespace::text AND table_name = @table_name::text;

-- name: ListTableMaintenancePolicies :many
SELECT * FROM table_maintenance_policies
WHERE catalog_id = @catalog_id::uuid
ORDER BY namespace, table_name;

-- name: ListManagedMaintenancePolicies :many
SELECT * FROM table_maintenance_policies
WHERE catalog_id = @catalog_id::uuid AND mode = 'managed'
ORDER BY namespace, table_name;

-- name: CreateTableMaintenancePolicy :one
-- No row comes back when the table already has parameters.
INSERT INTO table_maintenance_policies (
    catalog_id, namespace, table_name, mode, writer_commit_interval_seconds,
    optimize, optimize_manifests, expire_snapshots, remove_orphan_files, created_by, updated_by)
VALUES (
    @catalog_id::uuid, @namespace::text, @table_name::text, @mode::text, @writer_commit_interval_seconds::int,
    @optimize::jsonb, @optimize_manifests::jsonb, @expire_snapshots::jsonb, @remove_orphan_files::jsonb,
    sqlc.narg(actor)::uuid, sqlc.narg(actor)::uuid)
ON CONFLICT (catalog_id, namespace, table_name) DO NOTHING
RETURNING *;

-- name: UpdateTableMaintenancePolicy :one
-- No row comes back when the revision moved on (optimistic concurrency).
UPDATE table_maintenance_policies
SET mode = @mode::text,
    writer_commit_interval_seconds = @writer_commit_interval_seconds::int,
    optimize = @optimize::jsonb,
    optimize_manifests = @optimize_manifests::jsonb,
    expire_snapshots = @expire_snapshots::jsonb,
    remove_orphan_files = @remove_orphan_files::jsonb,
    revision = revision + 1,
    updated_by = sqlc.narg(actor)::uuid,
    updated_at = now()
WHERE catalog_id = @catalog_id::uuid AND namespace = @namespace::text AND table_name = @table_name::text
  AND revision = @revision::bigint
RETURNING *;

-- name: DeleteTableMaintenancePolicy :one
DELETE FROM table_maintenance_policies
WHERE catalog_id = @catalog_id::uuid AND namespace = @namespace::text AND table_name = @table_name::text
RETURNING *;

-- name: LastSuccessfulMaintenanceByOperation :many
-- The latest finished run of each operation on a table.
SELECT DISTINCT ON (op) op::text AS operation, finished_at
FROM maintenance_runs, unnest(operations) AS op
WHERE catalog_id = @catalog_id::uuid AND namespace = @namespace::text AND table_name = @table_name::text
  AND status = 'finished' AND finished_at IS NOT NULL
ORDER BY op, finished_at DESC;

-- name: ListCatalogTableHealth :many
SELECT * FROM table_health_snapshots WHERE catalog_id = @catalog_id::uuid;

-- name: ListCatalogLastSuccessfulMaintenance :many
-- LastSuccessfulMaintenanceByOperation for every table of a catalog.
SELECT DISTINCT ON (namespace, table_name, op) namespace, table_name, op::text AS operation, finished_at
FROM maintenance_runs, unnest(operations) AS op
WHERE catalog_id = @catalog_id::uuid AND status = 'finished' AND finished_at IS NOT NULL
ORDER BY namespace, table_name, op, finished_at DESC;

-- name: ListCatalogLatestMaintenanceRuns :many
-- Each table's most recent run.
SELECT DISTINCT ON (namespace, table_name) *
FROM maintenance_runs
WHERE catalog_id = @catalog_id::uuid
ORDER BY namespace, table_name, started_at DESC;

-- name: ListCatalogRunningMaintenance :many
SELECT namespace, table_name FROM maintenance_runs
WHERE catalog_id = @catalog_id::uuid AND status = 'running';

-- name: GetRunningMaintenanceRun :one
SELECT * FROM maintenance_runs
WHERE catalog_id = @catalog_id::uuid AND namespace = @namespace::text AND table_name = @table_name::text
  AND status = 'running';

-- name: ListTableMaintenanceRuns :many
SELECT * FROM maintenance_runs
WHERE catalog_id = @catalog_id::uuid AND namespace = @namespace::text AND table_name = @table_name::text
ORDER BY started_at DESC
LIMIT @lim::int;

-- name: CreatePlannedMaintenanceRun :one
-- A run with its trigger, reason and frozen parameters. A dry run is
-- recorded finished at once; any other run starts 'running'.
INSERT INTO maintenance_runs (
    catalog_id, binding_id, namespace, table_name, operations, retention, status,
    started_by, workflow_id, trigger, reason, parameters, policy_revision, finished_at)
VALUES (
    @catalog_id::uuid, sqlc.narg(binding_id)::uuid, @namespace::text, @table_name::text,
    @operations::text[], sqlc.narg(retention)::text,
    CASE WHEN @trigger::text = 'dry_run' THEN 'dry_run' ELSE 'running' END,
    sqlc.narg(started_by)::uuid, @workflow_id::text, @trigger::text, @reason::jsonb, @parameters::jsonb,
    sqlc.narg(policy_revision)::bigint,
    CASE WHEN @trigger::text = 'dry_run' THEN now() END)
RETURNING *;

-- name: GetTableHealthByTable :one
SELECT * FROM table_health_snapshots
WHERE catalog_id = @catalog_id::uuid AND namespace = @namespace::text AND table_name = @table_name::text;

-- name: GetAppliedCatalogRetention :one
-- The properties Baleia wrote for the binding's catalog on its engine's
-- cluster, and whether the plugin confirmed loading them (#500).
SELECT r.properties, r.updated_by
  FROM trino_catalog_registry r
  JOIN trino_clusters c ON c.id = r.cluster_id
 WHERE c.engine_id = @engine_id::uuid AND r.catalog_name = @catalog_name::text;

-- name: LockMaintenanceCapacity :exec
-- Serializes the rounds' capacity check and run creation until the
-- transaction ends, so concurrent catalogs cannot overfill the limit.
SELECT pg_advisory_xact_lock(hashtext('maintenance-capacity'));

-- name: CountRunningMaintenanceRuns :one
SELECT count(*)::int FROM maintenance_runs WHERE status = 'running';

-- name: SetMaintenanceRunAttempts :exec
UPDATE maintenance_runs SET attempts = @attempts::int WHERE id = @id::uuid;
