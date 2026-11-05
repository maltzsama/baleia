-- Popular tables from query history (B7, #562).

-- name: DeleteTablePopularity :exec
-- The refresh replaces every row in one transaction with InsertTablePopularity.
DELETE FROM table_popularity;

-- name: InsertTablePopularity :exec
-- Counts the reads of the last 30 days with a 7-day half-life decay. Only
-- finished statements and references with a catalog (a target) count.
INSERT INTO table_popularity (workspace_id, catalog_id, namespace, table_name, uses, score, last_used_at, computed_at)
SELECT r.workspace_id, r.catalog_id, r.namespace, r.table_name,
       count(*)::bigint AS uses,
       SUM(exp(-ln(2) * (EXTRACT(EPOCH FROM (now() - r.used_at)) / (7 * 86400.0))))::float8 AS score,
       max(r.used_at) AS last_used_at,
       now()
FROM (
    SELECT sr.workspace_id, sr.catalog_id, sr.namespace, sr.table_name,
           COALESCE(qs.finished_at, qs.started_at) AS used_at
    FROM statement_references sr
    JOIN query_statements qs ON qs.id = sr.statement_id
    WHERE sr.kind = 'read'
      AND sr.catalog_id IS NOT NULL
      AND qs.status = 'finished'
      AND COALESCE(qs.finished_at, qs.started_at) >= now() - interval '30 days'
) r
GROUP BY r.workspace_id, r.catalog_id, r.namespace, r.table_name;

-- name: ListPopularTables :many
-- The workspace's most used tables, live and granted only, best first.
SELECT t.id AS table_id, t.catalog_id, t.namespace, t.name, t.kind,
       p.uses, p.score, p.last_used_at,
       (SELECT max(r.finished_at) FROM metadata_sync_runs r WHERE r.catalog_id = t.catalog_id AND r.status = 'finished')::timestamptz AS metadata_synced_at
FROM table_popularity p
JOIN metadata_tables t
  ON t.catalog_id = p.catalog_id
 AND COALESCE(t.namespace[cardinality(t.namespace)], '') = p.namespace
 AND t.name = p.table_name
 AND t.removed_at IS NULL
WHERE p.workspace_id = @workspace_id::uuid
  AND t.catalog_id IN (
      SELECT baleia_workspace_catalog_ids(@workspace_id::uuid) AS id
  )
ORDER BY p.score DESC, p.last_used_at DESC, t.name
LIMIT @lim;
