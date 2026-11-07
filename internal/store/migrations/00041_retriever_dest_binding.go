package migrations

import (
	"context"

	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(up00041, down00041)
}

const up00041Postgres = `
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
`

const up00041MySQL = `
ALTER TABLE retrievers
    ADD COLUMN dest_binding_id CHAR(36) REFERENCES catalog_bindings (id) ON DELETE SET NULL,
    ADD COLUMN error_code TEXT;

ALTER TABLE retriever_refresh_history ADD COLUMN error_code TEXT;

UPDATE retrievers r
SET dest_binding_id = b.id
FROM catalog_bindings b
JOIN trino_clusters t ON t.engine_id = b.engine_id
WHERE t.name = 'default' AND b.engine_catalog_name = r.dest_catalog;

CREATE INDEX idx_retrievers_dest_binding ON retrievers (dest_binding_id);
`

const down00041Postgres = `
DROP INDEX IF EXISTS idx_retrievers_dest_binding;
ALTER TABLE retriever_refresh_history DROP COLUMN IF EXISTS error_code;
ALTER TABLE retrievers
    DROP COLUMN IF EXISTS error_code,
    DROP COLUMN IF EXISTS dest_binding_id;
`

const down00041MySQL = `
DROP INDEX IF EXISTS idx_retrievers_dest_binding;
ALTER TABLE retriever_refresh_history DROP COLUMN error_code;
ALTER TABLE retrievers
    DROP COLUMN error_code,
    DROP COLUMN dest_binding_id;
`

func up00041(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, up00041Postgres, up00041MySQL)
}

func down00041(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, down00041Postgres, down00041MySQL)
}
