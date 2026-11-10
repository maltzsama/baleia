-- Seed used by scripts/smoke.sh: expose the built-in `tpch_test` Trino catalog
-- (created by db/seed/tpch_test.sql) to the smoke admin's personal workspace.
--
-- Why this exists: POST /workspaces/{id}/executions resolves a workspace
-- *target* (catalog + binding + grant), not just the Trino registry row. This
-- mirrors cmd/e2e-seed's ensureTarget with the same query shapes, but in SQL so
-- it works from both compose and minikube. Idempotent.
--
-- The GORM store (ADR 0020) generates catalog and binding ids in the
-- application, so these raw inserts supply them with gen_random_uuid().
--
-- Usage (EMAIL is the smoke admin created by scripts/smoke.sh):
--   psql -U baleia -d baleia -v email="$EMAIL" < db/seed/smoke_target.sql

WITH eng AS (
    SELECT e.id AS engine_id, e.connection_id
    FROM engines e
    JOIN trino_clusters tc ON tc.engine_id = e.id
    WHERE tc.name = 'default'
), catalog AS (
    INSERT INTO catalogs (id, connection_id, name, origin, enabled)
    SELECT gen_random_uuid(), connection_id, 'tpch_test', 'native', true FROM eng
    ON CONFLICT (connection_id, name) DO UPDATE SET updated_at = now()
    RETURNING id
), binding AS (
    INSERT INTO catalog_bindings (id, catalog_id, engine_id, engine_catalog_name, mode)
    SELECT gen_random_uuid(), catalog.id, eng.engine_id, 'tpch_test', 'native' FROM catalog, eng
    ON CONFLICT (catalog_id, engine_id) DO UPDATE
        SET engine_catalog_name = EXCLUDED.engine_catalog_name,
            enabled             = true,
            updated_at          = now()
    RETURNING catalog_id
)
INSERT INTO workspace_catalog_grants (workspace_id, catalog_id, granted_by)
SELECT w.id, binding.catalog_id, u.id
FROM binding
JOIN users u ON u.email = :'email'
JOIN workspaces w ON w.owner_user_id = u.id AND w.type = 'personal'
ON CONFLICT (workspace_id, catalog_id) DO UPDATE
    SET granted_by = EXCLUDED.granted_by,
        granted_at = now();
