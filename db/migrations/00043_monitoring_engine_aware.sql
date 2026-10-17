-- +goose Up

-- Monitoring is per engine and table health per logical catalog (#117).
ALTER TABLE cluster_snapshots     ADD COLUMN engine_id UUID REFERENCES engines (id) ON DELETE CASCADE;
ALTER TABLE trino_query_snapshots ADD COLUMN engine_id UUID REFERENCES engines (id) ON DELETE CASCADE;

UPDATE cluster_snapshots
SET engine_id = (SELECT e.id FROM engines e JOIN trino_clusters t ON t.engine_id = e.id WHERE t.name = 'default');
UPDATE trino_query_snapshots
SET engine_id = (SELECT e.id FROM engines e JOIN trino_clusters t ON t.engine_id = e.id WHERE t.name = 'default');

ALTER TABLE cluster_snapshots     ALTER COLUMN engine_id SET NOT NULL;
ALTER TABLE trino_query_snapshots ALTER COLUMN engine_id SET NOT NULL;

CREATE INDEX cluster_snapshots_engine_time     ON cluster_snapshots (engine_id, captured_at DESC);
CREATE INDEX trino_query_snapshots_engine_time ON trino_query_snapshots (engine_id, created_at DESC);

ALTER TABLE table_health_snapshots ADD COLUMN catalog_id UUID REFERENCES catalogs (id) ON DELETE CASCADE;
UPDATE table_health_snapshots h
SET catalog_id = b.catalog_id
FROM catalog_bindings b
JOIN catalogs c ON c.id = b.catalog_id
WHERE c.connection_id = h.catalog_connection_id AND b.engine_catalog_name = h.catalog;
DELETE FROM table_health_snapshots WHERE catalog_id IS NULL;
ALTER TABLE table_health_snapshots ALTER COLUMN catalog_id SET NOT NULL;
ALTER TABLE table_health_snapshots
    DROP CONSTRAINT IF EXISTS table_health_snapshots_catalog_connection_id_catalog_namespace_table_name_key;
ALTER TABLE table_health_snapshots
    ADD CONSTRAINT table_health_unique_catalog UNIQUE (catalog_id, namespace, table_name);

CREATE TABLE maintenance_runs (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    catalog_id    UUID NOT NULL REFERENCES catalogs (id) ON DELETE CASCADE,
    binding_id    UUID REFERENCES catalog_bindings (id) ON DELETE SET NULL,
    namespace     TEXT NOT NULL,
    table_name    TEXT NOT NULL,
    operations    TEXT[] NOT NULL CHECK (operations <@ ARRAY['optimize','expire_snapshots','remove_orphan_files']),
    retention     TEXT,
    status        TEXT NOT NULL CHECK (status IN ('running','finished','failed')),
    error_code    TEXT,
    error_message TEXT,
    started_by    UUID REFERENCES users (id) ON DELETE SET NULL,
    started_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at   TIMESTAMPTZ,
    workflow_id   TEXT NOT NULL
);
CREATE INDEX maintenance_runs_catalog ON maintenance_runs (catalog_id, started_at DESC);

-- +goose Down

DROP TABLE IF EXISTS maintenance_runs;
ALTER TABLE table_health_snapshots DROP CONSTRAINT IF EXISTS table_health_unique_catalog;
ALTER TABLE table_health_snapshots DROP COLUMN IF EXISTS catalog_id;
DROP INDEX IF EXISTS trino_query_snapshots_engine_time;
DROP INDEX IF EXISTS cluster_snapshots_engine_time;
ALTER TABLE trino_query_snapshots DROP COLUMN IF EXISTS engine_id;
ALTER TABLE cluster_snapshots DROP COLUMN IF EXISTS engine_id;
