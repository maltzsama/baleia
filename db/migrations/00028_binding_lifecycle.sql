-- +goose Up
-- Bindings own their desired state and lifecycle (#38). The registry stays
-- exactly the plugin contract; what engines report goes to
-- engine_catalog_observations.
ALTER TABLE catalog_bindings
    ADD COLUMN lifecycle_state    TEXT NOT NULL DEFAULT 'active'
        CHECK (lifecycle_state IN ('provisioning', 'active', 'updating', 'error', 'deprovisioning', 'disabled')),
    ADD COLUMN desired_version    TEXT,
    ADD COLUMN last_error_code    TEXT,
    ADD COLUMN last_error_message TEXT,
    ADD COLUMN state_changed_at   TIMESTAMPTZ NOT NULL DEFAULT now();

UPDATE catalog_bindings SET lifecycle_state = CASE WHEN enabled THEN 'active' ELSE 'disabled' END;

-- The desired version of existing managed bindings is the registry's.
UPDATE catalog_bindings b
   SET desired_version = r.catalog_version
  FROM trino_clusters tc, trino_catalog_registry r
 WHERE b.mode = 'managed' AND tc.engine_id = b.engine_id
   AND r.cluster_id = tc.id AND r.catalog_name = b.engine_catalog_name;

CREATE TABLE engine_catalog_observations (
    engine_id     UUID NOT NULL REFERENCES engines (id) ON DELETE CASCADE,
    catalog_name  TEXT NOT NULL,
    present       BOOLEAN NOT NULL,
    mode          TEXT NOT NULL CHECK (mode IN ('managed', 'adopted', 'native')),
    connector     TEXT,
    first_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    observed_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (engine_id, catalog_name)
);

-- binding_status_v derives the sync status the API shows:
--   synced   active, the binder confirmed the desired version (Trino: the
--            plugin rewrote the registry row) and the engine does not
--            report it missing
--   pending  provisioning/updating, or active but not confirmed yet
--   error    error
--   disabled deprovisioning/disabled
-- Native and adopted bindings have no projection: active is synced.
CREATE VIEW binding_status_v AS
SELECT b.id AS binding_id,
       b.lifecycle_state,
       CASE
           WHEN b.lifecycle_state = 'error' THEN 'error'
           WHEN b.lifecycle_state IN ('deprovisioning', 'disabled') THEN 'disabled'
           WHEN b.lifecycle_state IN ('provisioning', 'updating') THEN 'pending'
           WHEN b.mode <> 'managed' THEN 'synced'
           WHEN r.updated_by = 'trino' AND r.enabled AND r.catalog_version = b.desired_version
                AND COALESCE(o.present, true) THEN 'synced'
           ELSE 'pending'
       END AS sync_status
FROM catalog_bindings b
LEFT JOIN trino_clusters tc ON tc.engine_id = b.engine_id
LEFT JOIN trino_catalog_registry r ON r.cluster_id = tc.id AND r.catalog_name = b.engine_catalog_name
LEFT JOIN engine_catalog_observations o ON o.engine_id = b.engine_id AND o.catalog_name = b.engine_catalog_name;

-- +goose Down
DROP VIEW binding_status_v;
DROP TABLE engine_catalog_observations;
ALTER TABLE catalog_bindings
    DROP COLUMN state_changed_at,
    DROP COLUMN last_error_message,
    DROP COLUMN last_error_code,
    DROP COLUMN desired_version,
    DROP COLUMN lifecycle_state;
