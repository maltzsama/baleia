-- +goose Up
-- A managed binding whose registry row the plugin marked 'error' (it could not
-- load the catalog at boot: bad properties, an unresolvable secret) is in
-- error, not pending forever; engine_error is the plugin's message (a generic
-- one when it gave none) in that state and '' otherwise, so it alone tells
-- the API the engine refused the catalog.
CREATE OR REPLACE VIEW binding_status_v AS
SELECT b.id AS binding_id,
       b.lifecycle_state,
       CASE
           WHEN b.lifecycle_state = 'error' THEN 'error'
           WHEN b.lifecycle_state IN ('deprovisioning', 'disabled') THEN 'disabled'
           WHEN b.lifecycle_state IN ('provisioning', 'updating') THEN 'pending'
           WHEN b.mode <> 'managed' THEN 'synced'
           WHEN r.enabled AND r.sync_status = 'error' THEN 'error'
           WHEN r.updated_by = 'trino' AND r.enabled AND r.catalog_version = b.desired_version
                AND COALESCE(o.present, true) THEN 'synced'
           ELSE 'pending'
       END AS sync_status,
       COALESCE(CASE
           WHEN b.lifecycle_state = 'active' AND b.mode = 'managed' AND r.enabled AND r.sync_status = 'error'
               THEN COALESCE(NULLIF(r.sync_error, ''), 'the engine could not load the catalog')
       END, '')::text AS engine_error
FROM catalog_bindings b
LEFT JOIN trino_clusters tc ON tc.engine_id = b.engine_id
LEFT JOIN trino_catalog_registry r ON r.cluster_id = tc.id AND r.catalog_name = b.engine_catalog_name
LEFT JOIN engine_catalog_observations o ON o.engine_id = b.engine_id AND o.catalog_name = b.engine_catalog_name;

-- +goose Down
DROP VIEW binding_status_v;
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
