-- +goose Up
-- Per-table maintenance parameters (#500). A table without a row is not
-- maintained. Each operation (Trino's optimize, optimize_manifests,
-- expire_snapshots, remove_orphan_files) has its own JSON settings: trigger,
-- min/max interval, window, timeout, conflict policy and params.
CREATE TABLE table_maintenance_policies (
    id                             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    catalog_id                     UUID NOT NULL REFERENCES catalogs (id) ON DELETE CASCADE,
    namespace                      TEXT NOT NULL,
    table_name                     TEXT NOT NULL,
    mode                           TEXT NOT NULL CHECK (mode IN ('managed', 'off', 'maintained_elsewhere')),
    -- The writer's commit or checkpoint interval: the hot-file and orphan
    -- windows must be longer than this.
    writer_commit_interval_seconds INTEGER NOT NULL DEFAULT 60 CHECK (writer_commit_interval_seconds > 0),
    optimize                       JSONB NOT NULL DEFAULT '{}'::jsonb,
    optimize_manifests             JSONB NOT NULL DEFAULT '{}'::jsonb,
    expire_snapshots               JSONB NOT NULL DEFAULT '{}'::jsonb,
    remove_orphan_files            JSONB NOT NULL DEFAULT '{}'::jsonb,
    revision                       BIGINT NOT NULL DEFAULT 1,
    created_by                     UUID REFERENCES users (id) ON DELETE SET NULL,
    updated_by                     UUID REFERENCES users (id) ON DELETE SET NULL,
    created_at                     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at                     TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (catalog_id, namespace, table_name)
);
CREATE INDEX table_maintenance_policies_managed
    ON table_maintenance_policies (catalog_id) WHERE mode = 'managed';

-- Runs record what triggered them, why they were due, the settings they
-- used (frozen) and how many attempts a commit conflict took.
ALTER TABLE maintenance_runs
    ADD COLUMN trigger         TEXT NOT NULL DEFAULT 'manual' CHECK (trigger IN ('manual', 'automatic', 'dry_run')),
    ADD COLUMN reason          JSONB NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN parameters      JSONB NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN attempts        INTEGER NOT NULL DEFAULT 1 CHECK (attempts > 0),
    ADD COLUMN policy_revision BIGINT;

ALTER TABLE maintenance_runs DROP CONSTRAINT maintenance_runs_status_check;
ALTER TABLE maintenance_runs ADD CONSTRAINT maintenance_runs_status_check
    CHECK (status IN ('running', 'finished', 'failed', 'gave_up', 'dry_run'));

ALTER TABLE maintenance_runs DROP CONSTRAINT maintenance_runs_operations_check;
ALTER TABLE maintenance_runs ADD CONSTRAINT maintenance_runs_operations_check
    CHECK (operations <@ ARRAY['optimize', 'optimize_manifests', 'expire_snapshots', 'remove_orphan_files']);

-- At most one running run per table, so manual and automatic runs never
-- overlap. Runs left 'running' by a crashed worker would break the index:
-- keep the newest per table and close the others.
UPDATE maintenance_runs r
SET status = 'failed', error_code = 'superseded', finished_at = COALESCE(r.finished_at, now())
WHERE r.status = 'running'
  AND EXISTS (
      SELECT 1 FROM maintenance_runs n
      WHERE n.status = 'running'
        AND n.catalog_id = r.catalog_id AND n.namespace = r.namespace AND n.table_name = r.table_name
        AND (n.started_at, n.id) > (r.started_at, r.id)
  );
CREATE UNIQUE INDEX maintenance_runs_one_running
    ON maintenance_runs (catalog_id, namespace, table_name) WHERE status = 'running';
CREATE INDEX maintenance_runs_table
    ON maintenance_runs (catalog_id, namespace, table_name, started_at DESC);

-- +goose Down
DROP INDEX IF EXISTS maintenance_runs_table;
DROP INDEX IF EXISTS maintenance_runs_one_running;
DELETE FROM maintenance_runs WHERE status = 'dry_run';
UPDATE maintenance_runs SET status = 'failed' WHERE status = 'gave_up';
-- Runs of optimize_manifests have no place in the old constraint.
DELETE FROM maintenance_runs WHERE 'optimize_manifests' = ANY (operations);
ALTER TABLE maintenance_runs DROP CONSTRAINT maintenance_runs_operations_check;
ALTER TABLE maintenance_runs ADD CONSTRAINT maintenance_runs_operations_check
    CHECK (operations <@ ARRAY['optimize', 'expire_snapshots', 'remove_orphan_files']);
ALTER TABLE maintenance_runs DROP CONSTRAINT maintenance_runs_status_check;
ALTER TABLE maintenance_runs ADD CONSTRAINT maintenance_runs_status_check
    CHECK (status IN ('running', 'finished', 'failed'));
ALTER TABLE maintenance_runs
    DROP COLUMN IF EXISTS policy_revision,
    DROP COLUMN IF EXISTS attempts,
    DROP COLUMN IF EXISTS parameters,
    DROP COLUMN IF EXISTS reason,
    DROP COLUMN IF EXISTS trigger;
DROP TABLE IF EXISTS table_maintenance_policies;
