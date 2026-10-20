-- +goose Up
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

-- +goose Down
ALTER TABLE content_versions DROP CONSTRAINT IF EXISTS content_versions_entity_type_check;
ALTER TABLE content_versions ADD CONSTRAINT content_versions_entity_type_check
    CHECK (entity_type IN ('saved_query', 'semantic_metric'));

DROP TABLE IF EXISTS semantic_models;
