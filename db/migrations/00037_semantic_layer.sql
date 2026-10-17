-- +goose Up

-- Semantic layer v1 (#33, D9 / ADR 0012): documentation, tags and owners on
-- tables and columns, plus simple structured metrics.
--
-- Docs are keyed by the *natural* key (catalog + namespace + table [+ column])
-- instead of metadata_tables.id, so they survive a metadata re-sync or a table
-- drop/recreate. They stay workspace-scoped: a doc describes a table, not a
-- content item, so #40 does not move them into spaces.
--
-- Metrics are content items: #40 adds space_id, #34 adds versions and bundles.

CREATE TABLE semantic_docs (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id  UUID NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    catalog_id    UUID NOT NULL REFERENCES catalogs (id) ON DELETE CASCADE,
    namespace     TEXT[] NOT NULL,
    table_name    TEXT NOT NULL,
    column_name   TEXT,                       -- NULL = table-level doc
    description   TEXT NOT NULL DEFAULT '' CHECK (char_length(description) <= 20000),
    tags          TEXT[] NOT NULL DEFAULT '{}',
    owner_user_id UUID REFERENCES users (id) ON DELETE SET NULL,
    updated_by    UUID NOT NULL REFERENCES users (id),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE NULLS NOT DISTINCT (workspace_id, catalog_id, namespace, table_name, column_name)
);

CREATE INDEX semantic_docs_lookup ON semantic_docs (workspace_id, catalog_id, namespace, table_name);
CREATE INDEX semantic_docs_description_trgm ON semantic_docs USING gin (description gin_trgm_ops);
CREATE INDEX semantic_docs_tags ON semantic_docs USING gin (tags);

CREATE TABLE semantic_metrics (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id   UUID NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    -- Portable identity (ADR 0013): bundles match metrics by (kind, slug),
    -- never by id or label.
    slug           TEXT NOT NULL CHECK (slug ~ '^[a-z][a-z0-9_]{0,62}$'),
    label          TEXT NOT NULL,
    description    TEXT NOT NULL DEFAULT '',
    certified      BOOLEAN NOT NULL DEFAULT false,
    catalog_id     UUID NOT NULL REFERENCES catalogs (id) ON DELETE CASCADE,
    namespace      TEXT[] NOT NULL,
    table_name     TEXT NOT NULL,
    binding_id     UUID REFERENCES catalog_bindings (id) ON DELETE SET NULL,
    -- Versioned structured definition (internal/semantic/simple.go). Never a
    -- free-form SQL string unless custom_sql is explicitly set.
    definition     JSONB NOT NULL,
    format_version INTEGER NOT NULL DEFAULT 1,
    owner_user_id  UUID REFERENCES users (id) ON DELETE SET NULL,
    updated_by     UUID NOT NULL REFERENCES users (id),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, slug)
);

CREATE INDEX semantic_metrics_table ON semantic_metrics (workspace_id, catalog_id, namespace, table_name);

-- +goose Down

DROP TABLE IF EXISTS semantic_metrics;
DROP TABLE IF EXISTS semantic_docs;
