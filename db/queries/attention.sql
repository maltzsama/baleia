-- Attention items (#261). Each query returns the raw candidates for one kind;
-- the handler filters them through the same authz predicates that guard the
-- fix, so an item is only shown to someone who can act on it.

-- name: ListAttentionBindingErrors :many
-- A binding in error, or stuck deprovisioning for more than the grace period.
SELECT b.id,
       b.catalog_id,
       c.name AS catalog_name,
       e.name AS engine_name,
       b.lifecycle_state,
       b.state_changed_at
FROM catalog_bindings b
JOIN catalogs c ON c.id = b.catalog_id
JOIN engines e ON e.id = b.engine_id
WHERE b.enabled AND c.enabled
  AND (b.lifecycle_state = 'error'
       OR (b.lifecycle_state = 'deprovisioning' AND b.state_changed_at < now() - interval '10 minutes'))
ORDER BY b.state_changed_at;

-- name: ListAttentionMetadataSyncFailed :many
-- The latest metadata pass of a catalog failed.
SELECT c.id AS catalog_id,
       c.name AS catalog_name,
       r.finished_at,
       r.error_code
FROM catalogs c
JOIN LATERAL (
    SELECT status, finished_at, error_code
    FROM metadata_sync_runs
    WHERE catalog_id = c.id
    ORDER BY started_at DESC
    LIMIT 1
) r ON true
WHERE c.enabled AND r.status = 'failed'
ORDER BY r.finished_at;

-- name: ListAttentionMetadataStale :many
-- A syncable catalog whose last finished pass is older than the threshold
-- (3 x the sync interval). A catalog no pass syncs is not stale.
SELECT c.id AS catalog_id,
       c.name AS catalog_name,
       COALESCE(
           (SELECT max(r.finished_at) FROM metadata_sync_runs r
            WHERE r.catalog_id = c.id AND r.status = 'finished'),
           c.created_at
       )::timestamptz AS since
FROM catalogs c
WHERE c.enabled
  AND (c.origin = 'source' OR EXISTS (
      SELECT 1 FROM catalog_bindings b WHERE b.catalog_id = c.id AND b.enabled))
  AND COALESCE(
          (SELECT max(r.finished_at) FROM metadata_sync_runs r
           WHERE r.catalog_id = c.id AND r.status = 'finished'),
          c.created_at
      ) < now() - make_interval(secs => $1)
ORDER BY since;

-- name: ListAttentionRetrieverErrors :many
-- A scheduled materialization whose last refresh failed.
SELECT r.id,
       r.name,
       r.workspace_id,
       w.name AS workspace_name,
       r.updated_at
FROM retrievers r
JOIN workspaces w ON w.id = r.workspace_id
WHERE r.status = 'error'
ORDER BY r.updated_at;

-- name: ListAttentionReconcileDrift :many
-- The latest dry run per engine reports mutating actions.
WITH latest AS (
    SELECT DISTINCT ON (engine_id) engine_id, finished_at, actions, error
    FROM catalog_reconcile_runs
    WHERE dry_run
    ORDER BY engine_id, finished_at DESC
)
SELECT l.engine_id,
       e.name AS engine_name,
       l.finished_at,
       jsonb_array_length(l.actions)::int AS action_count
FROM latest l
JOIN engines e ON e.id = l.engine_id
WHERE l.error IS NULL AND jsonb_array_length(l.actions) > 0
ORDER BY l.finished_at;

-- name: GetAttentionAuditExportLag :one
-- The age of the oldest audit event not yet exported (the lag the exporter
-- reports). NULL when nothing is pending.
SELECT COALESCE(
    (SELECT min(e.occurred_at)
     FROM audit_events e
     JOIN audit_export_cursor c ON c.sink = 'opensearch'
     WHERE (e.occurred_at, e.id) > (c.occurred_at, c.id)),
    (SELECT min(e.occurred_at)
     FROM audit_events e
     WHERE NOT EXISTS (SELECT 1 FROM audit_export_cursor WHERE sink = 'opensearch'))
)::timestamptz AS oldest_unexported;
