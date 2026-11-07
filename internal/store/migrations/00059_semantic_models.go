package migrations

import (
	"context"

	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(up00059, down00059)
}

const up00059Postgres = `
-- Semantic models (v2, #91): a queryable relation (table or SQL) with
-- dimensions, metrics and joins. Stored as a content item (space, slug,
-- versions, bundles) so it behaves like saved queries and metrics (#40/#34).
CREATE TABLE semantic_models (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id   UUID NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    space_id       UUID NOT NULL,
    slug           TEXT NOT NULL CHECK (slug ~ '^[a-z][a-z0-9_]{0,62}$'),
    label          TEXT NOT NULL,
    description    TEXT NOT NULL DEFAULT '',
    certified      BOOLEAN NOT NULL DEFAULT false,
    certified_by   UUID REFERENCES users (id) ON DELETE SET NULL,
    certified_at   TIMESTAMPTZ,
    -- Denormalized from the definition for catalog-grant checks and listings.
    catalog_id     UUID NOT NULL REFERENCES catalogs (id) ON DELETE CASCADE,
    binding_id     UUID REFERENCES catalog_bindings (id) ON DELETE SET NULL,
    -- The v2 model (internal/semantic/model.go): source, primary key,
    -- dimensions, metrics and joins. Never raw SQL from the caller beyond the
    -- model's declared SQL source/expressions.
    definition     JSONB NOT NULL,
    format_version INTEGER NOT NULL DEFAULT 1,
    owner_user_id  UUID REFERENCES users (id) ON DELETE SET NULL,
    updated_by     UUID NOT NULL REFERENCES users (id),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, slug),
    CONSTRAINT semantic_models_space_fk FOREIGN KEY (workspace_id, space_id)
        REFERENCES spaces (workspace_id, id) ON DELETE CASCADE
);

CREATE INDEX semantic_models_space ON semantic_models (space_id);
CREATE INDEX semantic_models_catalog ON semantic_models (catalog_id);

-- Models are versioned like the other content kinds (#34).
ALTER TABLE content_versions DROP CONSTRAINT IF EXISTS content_versions_entity_type_check;
ALTER TABLE content_versions ADD CONSTRAINT content_versions_entity_type_check
    CHECK (entity_type IN ('saved_query', 'semantic_metric', 'semantic_model'));
`

const up00059MySQL = `CREATE TABLE semantic_models (id             CHAR(36) PRIMARY KEY DEFAULT (UUID()),
    workspace_id   CHAR(36) NOT NULL,
    space_id       CHAR(36) NOT NULL,
    slug           TEXT NOT NULL,
    label          TEXT NOT NULL,
    description    TEXT NOT NULL DEFAULT (''),
    certified      BOOLEAN NOT NULL DEFAULT false,
    certified_by   CHAR(36),
    certified_at   DATETIME(6),
    catalog_id     CHAR(36) NOT NULL,
    binding_id     CHAR(36),
    definition     JSON NOT NULL,
    format_version INTEGER NOT NULL DEFAULT 1,
    owner_user_id  CHAR(36),
    updated_by     CHAR(36) NOT NULL,
    updated_at     DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    UNIQUE (workspace_id, slug(255)),
    CONSTRAINT semantic_models_space_fk FOREIGN KEY (workspace_id, space_id)
        REFERENCES spaces (workspace_id, id) ON DELETE CASCADE,
    CONSTRAINT semantic_models_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE CASCADE,
    CONSTRAINT semantic_models_slug_check CHECK (slug REGEXP '^[a-z][a-z0-9_]{0,62}$'),
    CONSTRAINT semantic_models_certified_by_fkey FOREIGN KEY (certified_by) REFERENCES users (id) ON DELETE SET NULL,
    CONSTRAINT semantic_models_catalog_id_fkey FOREIGN KEY (catalog_id) REFERENCES catalogs (id) ON DELETE CASCADE,
    CONSTRAINT semantic_models_binding_id_fkey FOREIGN KEY (binding_id) REFERENCES catalog_bindings (id) ON DELETE SET NULL,
    CONSTRAINT semantic_models_owner_user_id_fkey FOREIGN KEY (owner_user_id) REFERENCES users (id) ON DELETE SET NULL,
    CONSTRAINT semantic_models_updated_by_fkey FOREIGN KEY (updated_by) REFERENCES users (id)) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
CREATE INDEX IF NOT EXISTS semantic_models_space ON semantic_models (space_id);
CREATE INDEX IF NOT EXISTS semantic_models_catalog ON semantic_models (catalog_id);
ALTER TABLE content_versions DROP CONSTRAINT IF EXISTS content_versions_entity_type_check;
ALTER TABLE content_versions ADD CONSTRAINT content_versions_entity_type_check
    CHECK (entity_type IN ('saved_query', 'semantic_metric', 'semantic_model'));`

const down00059Postgres = `
ALTER TABLE content_versions DROP CONSTRAINT IF EXISTS content_versions_entity_type_check;
ALTER TABLE content_versions ADD CONSTRAINT content_versions_entity_type_check
    CHECK (entity_type IN ('saved_query', 'semantic_metric'));

DROP TABLE IF EXISTS semantic_models;
`

const down00059MySQL = `ALTER TABLE content_versions DROP CONSTRAINT IF EXISTS content_versions_entity_type_check;
ALTER TABLE content_versions ADD CONSTRAINT content_versions_entity_type_check
    CHECK (entity_type IN ('saved_query', 'semantic_metric'));
DROP TABLE IF EXISTS semantic_models;`

func up00059(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, up00059Postgres, up00059MySQL)
}

func down00059(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, down00059Postgres, down00059MySQL)
}
