-- +goose Up

-- Engine-neutral vocabulary (#20): a statement's id on its engine is not a
-- Trino id once direct engines run statements. Lens keeps
-- trino_query_snapshots.trino_query_id: it monitors Trino.
ALTER TABLE query_executions RENAME COLUMN trino_query_id TO engine_query_id;
ALTER TABLE query_statements RENAME COLUMN trino_query_id TO engine_query_id;

-- query_handle is the engine's QueryHandle (no credentials), persisted as soon
-- as the engine assigns it so another process can cancel the statement.
-- result_sets lists the stored results of the statement, one per result set:
-- [{result_index, result_key, rows, bytes, truncated}].
ALTER TABLE query_statements
    ADD COLUMN query_handle JSONB,
    ADD COLUMN result_sets JSONB NOT NULL DEFAULT '[]'::jsonb;

UPDATE query_statements
SET result_sets = jsonb_build_array(jsonb_build_object(
        'result_index', 0, 'result_key', result_key,
        'rows', coalesce(rows_returned, 0), 'bytes', coalesce(result_bytes, 0), 'truncated', truncated))
WHERE result_key IS NOT NULL;

-- +goose Down

ALTER TABLE query_statements
    DROP COLUMN IF EXISTS result_sets,
    DROP COLUMN IF EXISTS query_handle;

ALTER TABLE query_statements RENAME COLUMN engine_query_id TO trino_query_id;
ALTER TABLE query_executions RENAME COLUMN engine_query_id TO trino_query_id;
