-- Join suggestions from query history (B1, #554).

-- name: ListJoinSuggestions :many
-- Equi-join conditions a workspace used between two tables over the last 90
-- days, ranked by a 30-day half-life decay. The pair is normalized so the
-- left column always belongs to @left_table and the right to @right_table,
-- whatever order the statements wrote them in.
WITH uses AS (
    SELECT sj.left_column, sj.right_column,
           COALESCE(qs.finished_at, qs.started_at) AS used_at,
           lr.namespace AS left_ns, lr.table_name AS left_table,
           rr.namespace AS right_ns, rr.table_name AS right_table
    FROM statement_joins sj
    JOIN query_statements qs ON qs.id = sj.statement_id
    JOIN statement_references lr
      ON lr.statement_id = sj.statement_id AND lower(lr.alias) = lower(sj.left_alias)
    JOIN statement_references rr
      ON rr.statement_id = sj.statement_id AND lower(rr.alias) = lower(sj.right_alias)
    WHERE sj.workspace_id = @workspace_id::uuid
      AND qs.status = 'finished'
      AND COALESCE(qs.finished_at, qs.started_at) >= now() - interval '90 days'
      AND (lr.catalog_id IS NULL OR lr.catalog_id = @catalog_id::uuid)
      AND (rr.catalog_id IS NULL OR rr.catalog_id = @catalog_id::uuid)
), pairs AS (
    SELECT
        CASE WHEN lower(left_ns) = lower(@left_ns::text) AND lower(left_table) = lower(@left_table::text)
             THEN left_column::text ELSE right_column::text END AS left_column,
        CASE WHEN lower(left_ns) = lower(@left_ns::text) AND lower(left_table) = lower(@left_table::text)
             THEN right_column::text ELSE left_column::text END AS right_column,
        used_at
    FROM uses
    WHERE (lower(left_ns) = lower(@left_ns::text) AND lower(left_table) = lower(@left_table::text)
           AND lower(right_ns) = lower(@right_ns::text) AND lower(right_table) = lower(@right_table::text))
       OR (lower(right_ns) = lower(@left_ns::text) AND lower(right_table) = lower(@left_table::text)
           AND lower(left_ns) = lower(@right_ns::text) AND lower(left_table) = lower(@right_table::text))
)
SELECT left_column, right_column, count(*)::bigint AS use_count,
       max(used_at)::timestamptz AS last_used_at,
       SUM(exp(-ln(2) * (EXTRACT(EPOCH FROM (now() - used_at)) / (30 * 86400.0))))::float8 AS score
FROM pairs
GROUP BY left_column, right_column
ORDER BY score DESC, last_used_at DESC
LIMIT @lim;

-- name: ListForeignKeyJoinSuggestions :many
-- Foreign keys between the two tables, oriented so the left column belongs to
-- @left_table and the right to @right_table. A key declared on the right
-- table (referencing the left) is flipped.
WITH fk AS (
    SELECT column_name, ref_column
    FROM metadata_foreign_keys
    WHERE table_id = @left_table_id::uuid
      AND ref_namespace = @right_namespace::text[]
      AND lower(ref_table) = lower(@right_table::text)
    UNION ALL
    SELECT ref_column, column_name
    FROM metadata_foreign_keys
    WHERE table_id = @right_table_id::uuid
      AND ref_namespace = @left_namespace::text[]
      AND lower(ref_table) = lower(@left_table::text)
)
SELECT DISTINCT column_name AS left_column, ref_column AS right_column
FROM fk
ORDER BY left_column, right_column;
