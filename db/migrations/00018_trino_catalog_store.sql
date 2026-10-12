-- +goose Up

-- ── clusters ─────────────────────────────────────────────────────────────────
CREATE TABLE trino_clusters (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name            TEXT NOT NULL UNIQUE,
    coordinator_url TEXT NOT NULL,
    node_version    TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ
);

INSERT INTO trino_clusters (name, coordinator_url)
VALUES ('default', 'http://trino:8080');

-- ── domain: catalogs (recreated with CR-014 rev.3 §4.1 columns) ─────────────
CREATE TABLE catalogs (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    connection_id       UUID NOT NULL REFERENCES catalog_connections(id) ON DELETE CASCADE,
    name                TEXT NOT NULL,
    description         TEXT,
    trino_catalog_name  TEXT NOT NULL,
    cluster_id          UUID NOT NULL REFERENCES trino_clusters(id),
    warehouse           TEXT NOT NULL,
    connector           TEXT NOT NULL DEFAULT 'iceberg',
    extra_props         JSONB NOT NULL DEFAULT '{}'::jsonb,
    enabled             BOOLEAN NOT NULL DEFAULT TRUE,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (connection_id, name),
    UNIQUE (cluster_id, trino_catalog_name),
    CONSTRAINT catalogs_trino_name_format
        CHECK (trino_catalog_name ~ '^[a-z][a-z0-9_]{0,62}$'),
    CONSTRAINT catalogs_trino_name_not_reserved
        CHECK (trino_catalog_name NOT IN ('system', 'jmx', 'tpch', 'tpcds', 'memory')),
    CONSTRAINT catalogs_connector_format
        CHECK (connector ~ '^[a-z][a-z0-9_]{0,62}$')
);

-- ── projection read by the baleia-trino-catalog-store plugin at Trino boot ───
CREATE TABLE trino_catalog_registry (
    cluster_id      UUID NOT NULL REFERENCES trino_clusters(id) ON DELETE CASCADE,
    catalog_name    TEXT NOT NULL,
    connector_name  TEXT NOT NULL,
    properties      JSONB NOT NULL,
    catalog_version TEXT,
    enabled         BOOLEAN NOT NULL DEFAULT TRUE,
    sync_status     TEXT NOT NULL DEFAULT 'pending',
    sync_error      TEXT,
    updated_by      TEXT NOT NULL DEFAULT 'baleia',
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (cluster_id, catalog_name),
    CONSTRAINT registry_catalog_name_format
        CHECK (catalog_name ~ '^[a-z][a-z0-9_]{0,62}$'),
    CONSTRAINT registry_connector_name_format
        CHECK (connector_name ~ '^[a-z][a-z0-9_]{0,62}$'),
    CONSTRAINT registry_sync_status_valid
        CHECK (sync_status IN ('pending', 'synced', 'error')),
    CONSTRAINT registry_updated_by_valid
        CHECK (updated_by IN ('baleia', 'trino'))
);

CREATE INDEX trino_catalog_registry_enabled_idx
    ON trino_catalog_registry (cluster_id) WHERE enabled;

COMMENT ON TABLE trino_catalog_registry IS
    'Flat projection read by the baleia-trino-catalog-store plugin at Trino boot. '
    'Written by Baleia (write-through) and by the plugin (CREATE/DROP CATALOG). '
    'NEVER change the format without versioning the plugin alongside.';

-- ── dedicated role for Trino to read/write the projection ────────────────────
-- The role is created without a password. The API sets it at boot from
-- BALEIA_TRINO_DB_PASSWORD (internal/bootstrap) — the same env var the
-- baleia-trino-catalog-store plugin reads — so no secret is committed here.
-- +goose StatementBegin
DO $baleia$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'baleia_trino') THEN
        CREATE ROLE baleia_trino LOGIN;
    END IF;
END
$baleia$;
-- +goose StatementEnd

GRANT USAGE ON SCHEMA public TO baleia_trino;
GRANT SELECT, INSERT, UPDATE, DELETE ON trino_catalog_registry TO baleia_trino;
GRANT SELECT ON trino_clusters TO baleia_trino;

-- +goose Down

REVOKE SELECT, INSERT, UPDATE, DELETE ON trino_catalog_registry FROM baleia_trino;
REVOKE SELECT ON trino_clusters FROM baleia_trino;
REVOKE USAGE ON SCHEMA public FROM baleia_trino;
DROP ROLE IF EXISTS baleia_trino;

DROP TABLE IF EXISTS trino_catalog_registry;
DROP TABLE IF EXISTS catalogs;
DROP TABLE IF EXISTS trino_clusters;