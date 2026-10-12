-- +goose Up
DELETE FROM saved_queries sq1 USING saved_queries sq2
WHERE sq1.created_at < sq2.created_at
  AND sq1.workspace_id = sq2.workspace_id
  AND sq1.name = sq2.name;

CREATE UNIQUE INDEX idx_saved_queries_unique_name ON saved_queries (workspace_id, name);

-- +goose Down
DROP INDEX IF EXISTS idx_saved_queries_unique_name;
