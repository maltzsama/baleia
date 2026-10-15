-- name: GetCatalogForSync :one
-- A catalog with what the sync needs to pick its source: the connection
-- kind and the reference inside it (warehouse, database).
SELECT c.id, c.name, c.origin, c.source_ref, c.warehouse, c.enabled,
       cc.id AS connection_id, cc.type AS connection_type
FROM catalogs c
JOIN catalog_connections cc ON cc.id = c.connection_id
WHERE c.id = $1;

-- name: ListSyncBindings :many
-- The catalog enabled bindings on enabled engines, oldest first: the
-- engine path introspects through the first one (a native catalog has only
-- its native binding).
SELECT b.id, b.engine_id, b.engine_catalog_name, b.mode, e.kind AS engine_kind
FROM catalog_bindings b
JOIN engines e ON e.id = b.engine_id
WHERE b.catalog_id = $1 AND b.enabled AND e.enabled
  AND b.lifecycle_state IN ('active', 'updating')
ORDER BY b.created_at, b.id;

-- name: StartMetadataSyncRun :one
INSERT INTO metadata_sync_runs (catalog_id, status, source_path)
VALUES ($1, 'running', $2)
RETURNING id, started_at;

-- name: FinishMetadataSyncRun :exec
UPDATE metadata_sync_runs
SET status = $2, finished_at = now(), tables_seen = $3, tables_changed = $4, tables_removed = $5,
    error_code = $6, error_message = $7
WHERE id = $1 AND status = 'running';

-- name: FailStaleMetadataSyncRuns :exec
-- A run left running by a crashed worker ends failed when the next pass of
-- the same catalog starts (that pass holds the catalog sync lock).
UPDATE metadata_sync_runs
SET status = 'failed', finished_at = now(), error_code = 'interrupted',
    error_message = 'the sync stopped before it finished'
WHERE catalog_id = $1 AND status = 'running';

-- name: GetTableFingerprints :many
-- What the last passes stored, so a source can skip loading unchanged tables.
SELECT namespace, name, fingerprint, properties
FROM metadata_tables
WHERE catalog_id = $1 AND removed_at IS NULL;

-- name: UpsertMetadataTable :one
-- Records a table seen by the pass. changed is true when the table is new,
-- came back from a tombstone or has a new fingerprint: its columns must be
-- replaced.
INSERT INTO metadata_tables AS t (catalog_id, namespace, name, kind, fingerprint, properties, comment, last_seen_at, last_changed_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, now(), now())
ON CONFLICT (catalog_id, namespace, name) DO UPDATE
SET kind = EXCLUDED.kind,
    fingerprint = EXCLUDED.fingerprint,
    properties = EXCLUDED.properties,
    comment = EXCLUDED.comment,
    last_seen_at = now(),
    last_changed_at = CASE
        WHEN t.fingerprint IS DISTINCT FROM EXCLUDED.fingerprint OR t.removed_at IS NOT NULL THEN now()
        ELSE t.last_changed_at
    END,
    removed_at = NULL
RETURNING id, (last_changed_at = now())::boolean AS changed;

-- name: TouchMetadataTable :execrows
-- A table the source reported unchanged (same fingerprint, columns not
-- loaded): it is still there.
UPDATE metadata_tables
SET last_seen_at = now()
WHERE catalog_id = $1 AND namespace = $2 AND name = $3 AND removed_at IS NULL;

-- name: DeleteMetadataColumns :exec
DELETE FROM metadata_columns WHERE table_id = $1;

-- name: InsertMetadataColumns :exec
INSERT INTO metadata_columns (table_id, ordinal, name, native_type, type_system, nullable, comment)
SELECT @table_id::uuid, c.ordinal, c.name, c.native_type, @type_system::text, c.nullable, NULLIF(c.comment, '')
FROM (
    SELECT unnest(@ordinals::int[]) AS ordinal, unnest(@names::text[]) AS name,
           unnest(@native_types::text[]) AS native_type, unnest(@nullables::boolean[]) AS nullable,
           unnest(@comments::text[]) AS comment
) AS c;

-- name: UpsertMetadataNamespace :exec
INSERT INTO metadata_namespaces (catalog_id, namespace, last_seen_at)
VALUES ($1, $2, now())
ON CONFLICT (catalog_id, namespace) DO UPDATE
SET last_seen_at = now(), removed_at = NULL;

-- name: TombstoneMissingTables :execrows
-- Tables the pass did not see were removed at the source.
UPDATE metadata_tables
SET removed_at = now()
WHERE catalog_id = $1 AND last_seen_at < @pass_started_at::timestamptz AND removed_at IS NULL;

-- name: TombstoneMissingNamespaces :exec
UPDATE metadata_namespaces
SET removed_at = now()
WHERE catalog_id = $1 AND last_seen_at < @pass_started_at::timestamptz AND removed_at IS NULL;

-- name: PurgeMetadataTableTombstones :exec
-- Tombstones older than 7 days go: the explorer shows "removed" for a while,
-- history does not grow forever.
DELETE FROM metadata_tables
WHERE catalog_id = $1 AND removed_at < now() - interval '7 days';

-- name: PurgeMetadataNamespaceTombstones :exec
DELETE FROM metadata_namespaces
WHERE catalog_id = $1 AND removed_at < now() - interval '7 days';

-- name: TryMetadataSyncLock :one
-- One sync per catalog at a time; a session-level lock released by
-- ReleaseMetadataSyncLock on the same connection.
SELECT pg_try_advisory_lock(hashtext('metadata-sync:' || sqlc.arg(catalog_id)::text));

-- name: ReleaseMetadataSyncLock :exec
SELECT pg_advisory_unlock(hashtext('metadata-sync:' || sqlc.arg(catalog_id)::text));

-- name: ListMetadataSyncRuns :many
SELECT id, catalog_id, status, source_path, started_at, finished_at, tables_seen, tables_changed, tables_removed, error_code
FROM metadata_sync_runs
WHERE catalog_id = $1
ORDER BY started_at DESC
LIMIT $2;

-- name: ListSyncableCatalogs :many
-- Every catalog the scheduled sync refreshes: enabled, and readable from its
-- source or through at least one enabled binding.
SELECT c.id
FROM catalogs c
WHERE c.enabled
  AND (c.origin = 'source' OR EXISTS (
      SELECT 1 FROM catalog_bindings b WHERE b.catalog_id = c.id AND b.enabled))
ORDER BY c.id;

-- name: GetCatalogSyncState :one
-- The freshness of a catalog snapshot: its latest pass and the end of its
-- latest finished one.
SELECT
    COALESCE((SELECT r.status FROM metadata_sync_runs r WHERE r.catalog_id = $1 ORDER BY r.started_at DESC LIMIT 1), '')::text AS last_status,
    COALESCE((SELECT r.error_code FROM metadata_sync_runs r WHERE r.catalog_id = $1 ORDER BY r.started_at DESC LIMIT 1), '')::text AS last_error_code,
    (SELECT max(r.finished_at) FROM metadata_sync_runs r WHERE r.catalog_id = $1 AND r.status = 'finished')::timestamptz AS synced_at;

-- name: MaxMetadataStalenessSeconds :one
-- Seconds since the stalest syncable catalog last finished a pass (never:
-- since it was created). One number, no catalog label (#87).
SELECT COALESCE(max(EXTRACT(EPOCH FROM now() - COALESCE(
    (SELECT max(r.finished_at) FROM metadata_sync_runs r WHERE r.catalog_id = c.id AND r.status = 'finished'),
    c.created_at))), 0)::float8
FROM catalogs c
WHERE c.enabled;
