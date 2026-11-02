-- Statement references and equi-joins (B0, #530, #538).

-- name: DeleteStatementReferences :exec
DELETE FROM statement_references WHERE statement_id = $1;

-- name: DeleteStatementJoins :exec
DELETE FROM statement_joins WHERE statement_id = $1;

-- name: InsertStatementReference :exec
INSERT INTO statement_references (statement_id, workspace_id, catalog_id, namespace, table_name, alias, kind)
VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: InsertStatementJoin :exec
INSERT INTO statement_joins (statement_id, workspace_id, left_alias, left_column, right_alias, right_column)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: ListStatementsForReferenceBackfill :many
-- Keyset-paginated one-off backfill (#538). `cursor` is the last id of the
-- previous page.
SELECT qs.id, qs.sql, qe.workspace_id,
       qe.target_catalog_id, COALESCE(qe.target_schema, '')::text AS target_schema
FROM query_statements qs
JOIN query_executions qe ON qe.id = qs.execution_id
WHERE qs.status = 'finished'
  AND (sqlc.narg(cursor)::uuid IS NULL OR qs.id > sqlc.narg(cursor)::uuid)
ORDER BY qs.id
LIMIT @lim;

-- name: GetStatementReferenceTarget :one
-- The statement row and the workspace it belongs to, for the live persistence
-- path. It reads the workspace from the execution so V3's activity input does
-- not change (#538).
SELECT s.id AS statement_id, e.workspace_id
FROM query_statements s
JOIN query_executions e ON e.id = s.execution_id
WHERE s.execution_id = $1 AND s.position = $2;
