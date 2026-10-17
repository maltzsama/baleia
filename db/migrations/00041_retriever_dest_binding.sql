-- +goose Up

-- Retrievers materialize into a binding, not into a catalog-name string (#118):
-- the binding names the engine that runs the refresh and the catalog to write.
ALTER TABLE retrievers
    ADD COLUMN dest_binding_id UUID REFERENCES catalog_bindings (id) ON DELETE SET NULL,
    ADD COLUMN error_code TEXT;

ALTER TABLE retriever_refresh_history ADD COLUMN error_code TEXT;

-- Backfill from the legacy dest_catalog against the default Trino engine.
UPDATE retrievers r
SET dest_binding_id = b.id
FROM catalog_bindings b
JOIN trino_clusters t ON t.engine_id = b.engine_id
WHERE t.name = 'default' AND b.engine_catalog_name = r.dest_catalog;

-- Unmapped retrievers cannot run: surface the reason instead of refreshing the
-- wrong catalog.
UPDATE retrievers
SET status = 'error', error_code = 'destination_unavailable'
WHERE dest_binding_id IS NULL;

CREATE INDEX idx_retrievers_dest_binding ON retrievers (dest_binding_id);

-- +goose Down

DROP INDEX IF EXISTS idx_retrievers_dest_binding;
ALTER TABLE retriever_refresh_history DROP COLUMN IF EXISTS error_code;
ALTER TABLE retrievers
    DROP COLUMN IF EXISTS error_code,
    DROP COLUMN IF EXISTS dest_binding_id;
