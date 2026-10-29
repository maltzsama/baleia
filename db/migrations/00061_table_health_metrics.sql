-- +goose Up
-- Health metrics that tell a CDC or streaming table from a batch one (#500),
-- the operations a table's parameters find due, and the Iceberg properties
-- the form starts from. needs_orphan_cleanup was never computed: orphan
-- cleanup is driven by its maximum interval instead.
ALTER TABLE table_health_snapshots
    ADD COLUMN delete_file_count     BIGINT,
    ADD COLUMN equality_delete_count BIGINT,
    ADD COLUMN position_delete_count BIGINT,
    ADD COLUMN small_file_count      BIGINT,
    ADD COLUMN commits_last_hour     INTEGER,
    ADD COLUMN commits_last_day      INTEGER,
    ADD COLUMN avg_file_size_bytes   BIGINT,
    ADD COLUMN manifest_count        INTEGER,
    ADD COLUMN format_version        INTEGER,
    ADD COLUMN table_properties      JSONB NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN due_operations        TEXT[] NOT NULL DEFAULT '{}',
    ADD COLUMN policy_revision       BIGINT;
ALTER TABLE table_health_snapshots DROP COLUMN needs_orphan_cleanup;

-- +goose Down
ALTER TABLE table_health_snapshots ADD COLUMN needs_orphan_cleanup BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE table_health_snapshots
    DROP COLUMN IF EXISTS policy_revision,
    DROP COLUMN IF EXISTS due_operations,
    DROP COLUMN IF EXISTS table_properties,
    DROP COLUMN IF EXISTS format_version,
    DROP COLUMN IF EXISTS manifest_count,
    DROP COLUMN IF EXISTS avg_file_size_bytes,
    DROP COLUMN IF EXISTS commits_last_day,
    DROP COLUMN IF EXISTS commits_last_hour,
    DROP COLUMN IF EXISTS small_file_count,
    DROP COLUMN IF EXISTS position_delete_count,
    DROP COLUMN IF EXISTS equality_delete_count,
    DROP COLUMN IF EXISTS delete_file_count;
