-- name: ListBindingsByCatalog :many
SELECT * FROM catalog_bindings WHERE catalog_id = $1 ORDER BY created_at;

-- name: ListBindingsByEngine :many
SELECT * FROM catalog_bindings WHERE engine_id = $1 ORDER BY engine_catalog_name;

-- name: GetBindingForTarget :one
-- Everything an execution target needs about a binding: its engine and its
-- logical catalog.
SELECT
    b.id          AS binding_id,
    b.engine_catalog_name,
    b.enabled     AS binding_enabled,
    b.mode,
    e.id          AS engine_id,
    e.name        AS engine_name,
    e.kind        AS engine_kind,
    e.enabled     AS engine_enabled,
    c.id          AS catalog_id,
    c.name        AS catalog_name,
    c.enabled     AS catalog_enabled
FROM catalog_bindings b
JOIN engines e ON e.id = b.engine_id
JOIN catalogs c ON c.id = b.catalog_id
WHERE b.id = $1;

-- name: SetWorkspaceDefaultTarget :exec
UPDATE workspaces
SET default_binding_id = $2, default_schema = $3, updated_at = now()
WHERE id = $1;

-- name: SetExecutionTarget :exec
-- Stores the resolved target of an execution. target_snapshot is immutable
-- context for history and carries no secret.
UPDATE query_executions
SET engine_id         = $2,
    target_binding_id = $3,
    target_catalog_id = $4,
    target_schema     = $5,
    target_snapshot   = $6
WHERE id = $1;

-- name: UpsertNativeCatalog :one
-- A catalog found inside an engine, owned by the engine's connection.
INSERT INTO catalogs (connection_id, name, origin, enabled)
VALUES ($1, $2, 'native', true)
ON CONFLICT (connection_id, name) DO UPDATE SET updated_at = now()
RETURNING *;

-- name: UpsertNativeBinding :one
-- A native binding is enabled while its engine reports the catalog: native
-- bindings are only ever disabled by discovery (DisableMissingNativeBindings).
INSERT INTO catalog_bindings (catalog_id, engine_id, engine_catalog_name, mode)
VALUES ($1, $2, $3, 'native')
ON CONFLICT (catalog_id, engine_id) DO UPDATE SET
    engine_catalog_name = EXCLUDED.engine_catalog_name,
    enabled             = true,
    updated_at          = now()
RETURNING *;

-- name: DisableMissingNativeBindings :execrows
-- Native catalogs an engine no longer reports are disabled, never deleted
-- (#17): their metadata and history stay.
UPDATE catalog_bindings
SET enabled = false, updated_at = now()
WHERE engine_id = $1 AND mode = 'native' AND enabled
  AND NOT (engine_catalog_name = ANY(sqlc.arg(present)::text[]));

-- name: DeleteNativeCatalogsOfEngine :exec
-- The native catalogs of an engine, deleted with it (their bindings cascade).
DELETE FROM catalogs c
USING catalog_bindings b
WHERE b.catalog_id = c.id AND b.engine_id = $1 AND b.mode = 'native' AND c.origin = 'native';

-- name: ListWorkspaceTargets :many
-- Every binding of a catalog the workspace may use, with the strongest
-- permission it holds on that catalog: 'write' for the catalogs of the owner
-- of a personal workspace (their own connections), else the grant (#16).
-- Disabled bindings, engines and catalogs are included; callers filter.
WITH visible AS (
    SELECT c.id AS catalog_id, 'write'::text AS permission
    FROM catalogs c
    JOIN catalog_connections cc ON cc.id = c.connection_id
    JOIN workspaces w ON w.id = sqlc.arg('workspace_id')
    WHERE w.type = 'personal' AND w.owner_user_id IS NOT NULL AND cc.created_by = w.owner_user_id
    UNION ALL
    SELECT g.catalog_id, g.permission
    FROM workspace_catalog_grants g
    WHERE g.workspace_id = sqlc.arg('workspace_id')
), perm AS (
    SELECT catalog_id,
           (CASE WHEN bool_or(permission = 'write') THEN 'write' ELSE 'read' END)::text AS permission
    FROM visible
    GROUP BY catalog_id
)
SELECT
    b.id                  AS binding_id,
    b.engine_catalog_name,
    b.mode,
    b.enabled             AS binding_enabled,
    e.id                  AS engine_id,
    e.name                AS engine_name,
    e.kind                AS engine_kind,
    e.enabled             AS engine_enabled,
    c.id                  AS catalog_id,
    c.name                AS catalog_name,
    c.enabled             AS catalog_enabled,
    p.permission
FROM perm p
JOIN catalogs c ON c.id = p.catalog_id
JOIN catalog_bindings b ON b.catalog_id = c.id
JOIN engines e ON e.id = b.engine_id
ORDER BY c.name, e.name;

-- name: GetBinding :one
SELECT * FROM catalog_bindings WHERE id = $1;

-- name: UpsertManagedBinding :one
-- The managed binding of a catalog on an engine (one per pair), entering
-- lifecycle_state with the projection version it wants (#38).
INSERT INTO catalog_bindings (catalog_id, engine_id, engine_catalog_name, connector, extra_props, mode, enabled,
                              lifecycle_state, desired_version, last_error_code, last_error_message, state_changed_at)
VALUES ($1, $2, $3, $4, $5, 'managed', $6, sqlc.arg(lifecycle_state), sqlc.narg(desired_version), NULL, NULL, now())
ON CONFLICT (catalog_id, engine_id) DO UPDATE SET
    engine_catalog_name = EXCLUDED.engine_catalog_name,
    connector           = EXCLUDED.connector,
    extra_props         = EXCLUDED.extra_props,
    enabled             = EXCLUDED.enabled,
    lifecycle_state     = EXCLUDED.lifecycle_state,
    desired_version     = EXCLUDED.desired_version,
    last_error_code     = NULL,
    last_error_message  = NULL,
    state_changed_at    = now(),
    updated_at          = now()
RETURNING *;

-- name: CreateManagedBinding :one
INSERT INTO catalog_bindings (catalog_id, engine_id, engine_catalog_name, connector, extra_props, mode, enabled,
                              lifecycle_state, desired_version)
VALUES ($1, $2, $3, $4, $5, 'managed', true, 'provisioning', sqlc.narg(desired_version))
RETURNING *;

-- name: UpdateBinding :one
-- Writes the desired state and enters lifecycle_state, guarded by the states
-- a change may start from; no row means a concurrent change.
UPDATE catalog_bindings SET
    engine_catalog_name = $2,
    connector           = $3,
    extra_props         = $4,
    enabled             = $5,
    lifecycle_state     = sqlc.arg(lifecycle_state),
    desired_version     = COALESCE(sqlc.narg(desired_version), desired_version),
    last_error_code     = NULL,
    last_error_message  = NULL,
    state_changed_at    = now(),
    updated_at          = now()
WHERE id = $1 AND lifecycle_state = ANY(sqlc.arg(from_states)::text[])
RETURNING *;

-- name: DeleteBinding :exec
DELETE FROM catalog_bindings WHERE id = $1;

-- name: DisableManagedBindingsOfCatalog :exec
UPDATE catalog_bindings SET enabled = false, updated_at = now()
WHERE catalog_id = $1 AND mode = 'managed';

-- name: GetTrinoClusterByEngine :one
SELECT * FROM trino_clusters WHERE engine_id = $1;

-- name: TransitionBinding :one
-- Moves a binding between lifecycle states, guarded by the expected ones; no
-- row means a concurrent change (#38).
UPDATE catalog_bindings SET
    lifecycle_state    = sqlc.arg(to_state),
    last_error_code    = sqlc.narg(error_code),
    last_error_message = sqlc.narg(error_message),
    engine_catalog_name = COALESCE(sqlc.narg(engine_catalog_name), engine_catalog_name),
    desired_version    = COALESCE(sqlc.narg(desired_version), desired_version),
    state_changed_at   = now(),
    updated_at         = now()
WHERE id = sqlc.arg(id) AND lifecycle_state = ANY(sqlc.arg(from_states)::text[])
RETURNING *;

-- name: GetBindingStatus :one
SELECT * FROM binding_status_v WHERE binding_id = $1;

-- name: ListBindingStatusByCatalog :many
SELECT s.* FROM binding_status_v s JOIN catalog_bindings b ON b.id = s.binding_id WHERE b.catalog_id = $1;

-- name: ManagedBindingNamesOfEngine :many
SELECT engine_catalog_name FROM catalog_bindings WHERE engine_id = $1 AND mode = 'managed';

-- name: UpsertEngineCatalogObservation :exec
INSERT INTO engine_catalog_observations (engine_id, catalog_name, present, mode, connector)
VALUES ($1, $2, true, $3, $4)
ON CONFLICT (engine_id, catalog_name) DO UPDATE SET
    present     = true,
    mode        = EXCLUDED.mode,
    connector   = EXCLUDED.connector,
    observed_at = now();

-- name: MarkEngineCatalogsAbsent :exec
-- Catalogs the engine no longer reports.
UPDATE engine_catalog_observations SET present = false, observed_at = now()
WHERE engine_id = $1 AND NOT (catalog_name = ANY(sqlc.arg(present_names)::text[]));

-- name: UpsertAdoptedBinding :exec
-- A catalog found in an engine that no managed binding created (#38, #13).
-- An adopted binding the engine reports again is enabled again; a managed
-- binding of the same name is never touched.
INSERT INTO catalog_bindings AS b (catalog_id, engine_id, engine_catalog_name, connector, mode, enabled, lifecycle_state)
VALUES ($1, $2, $3, $4, 'adopted', true, 'active')
ON CONFLICT (engine_id, engine_catalog_name) DO UPDATE SET enabled = true, updated_at = now()
WHERE b.mode = 'adopted' AND NOT b.enabled;

-- name: DisableMissingAdoptedBindings :execrows
-- Adopted catalogs the engine no longer reports are disabled, never deleted
-- (#17): their metadata and history stay.
UPDATE catalog_bindings b
SET enabled = false, updated_at = now()
FROM engine_catalog_observations o
WHERE b.engine_id = $1 AND b.mode = 'adopted' AND b.enabled
  AND o.engine_id = b.engine_id AND o.catalog_name = b.engine_catalog_name AND NOT o.present;

-- name: GetManagedBindingOfCatalog :one
SELECT * FROM catalog_bindings WHERE catalog_id = $1 AND engine_id = $2 AND mode = 'managed';

-- name: ObservedManagedNamesOfEngine :many
-- Catalogs Baleia managed and still sees in the engine, though no managed
-- binding owns them any more (an old name that failed to drop): they stay
-- managed, never adopted, for the reconciler (#37) to drop.
SELECT catalog_name FROM engine_catalog_observations
WHERE engine_id = $1 AND mode = 'managed' AND present;

-- name: MarkEngineCatalogManaged :exec
INSERT INTO engine_catalog_observations (engine_id, catalog_name, present, mode)
VALUES ($1, $2, true, 'managed')
ON CONFLICT (engine_id, catalog_name) DO UPDATE SET mode = 'managed', present = true, observed_at = now();

-- name: CountManagedBindingsByState :many
SELECT lifecycle_state, count(*)::bigint AS count FROM catalog_bindings WHERE mode = 'managed' GROUP BY lifecycle_state;

-- name: ListManagedBindingsOfEngine :many
-- The managed bindings of an engine the reconciler checks (#37).
SELECT * FROM catalog_bindings
WHERE engine_id = $1 AND mode = 'managed'
ORDER BY engine_catalog_name;

-- name: InsertReconcileRun :one
WITH run AS (
    INSERT INTO catalog_reconcile_runs (engine_id, started_at, finished_at, dry_run, actions, adopted, error)
    VALUES ($1, $2, $3, $4, $5, $6, $7)
    RETURNING *
), pruned AS (
    DELETE FROM catalog_reconcile_runs
    WHERE engine_id = $1 AND id NOT IN (
        SELECT id FROM catalog_reconcile_runs WHERE engine_id = $1 ORDER BY started_at DESC LIMIT 99
    )
)
SELECT * FROM run;

-- name: ListReconcileRuns :many
SELECT * FROM catalog_reconcile_runs
WHERE engine_id = $1
ORDER BY started_at DESC
LIMIT $2;

-- name: GetReconcileRun :one
SELECT * FROM catalog_reconcile_runs WHERE engine_id = $1 AND id = $2;

-- name: LatestReconcileRuns :many
-- Newest run per engine for the system status section (#291).
SELECT DISTINCT ON (engine_id) engine_id, started_at, finished_at, dry_run, error
FROM catalog_reconcile_runs
ORDER BY engine_id, started_at DESC;
