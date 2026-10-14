-- +goose Up

-- The four-concept model (docs/adr/0002, #13): connections, engines, logical
-- catalogs and bindings. trino_clusters and trino_catalog_registry are the
-- Trino binder's tables and the plugin contract; only additive changes here.

CREATE TABLE engines (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name           TEXT NOT NULL UNIQUE CHECK (name ~ '^[a-z][a-z0-9_-]{0,62}$'),
    kind           TEXT NOT NULL,
    class          TEXT NOT NULL CHECK (class IN ('federated', 'direct')),
    connection_id  UUID NOT NULL UNIQUE REFERENCES catalog_connections (id) ON DELETE RESTRICT,
    identity_mode  TEXT NOT NULL DEFAULT 'connection'
                   CHECK (identity_mode IN ('impersonation', 'connection', 'per_user')),
    enabled        BOOLEAN NOT NULL DEFAULT true,
    created_by     UUID REFERENCES users (id) ON DELETE SET NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE trino_clusters
    ADD COLUMN engine_id UUID UNIQUE REFERENCES engines (id) ON DELETE RESTRICT;

-- A catalog is now logical: owned by a connection (source) or found inside an
-- engine (native). Its Trino columns stay NOT NULL until the first native
-- catalog is written (#13 PR 2), which relaxes them together with the code
-- that reads them; they are dropped once nothing reads them.
ALTER TABLE catalogs
    ADD COLUMN origin     TEXT NOT NULL DEFAULT 'source' CHECK (origin IN ('source', 'native')),
    ADD COLUMN source_ref TEXT;

CREATE TABLE catalog_bindings (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    catalog_id          UUID NOT NULL REFERENCES catalogs (id) ON DELETE CASCADE,
    engine_id           UUID NOT NULL REFERENCES engines (id) ON DELETE CASCADE,
    engine_catalog_name TEXT NOT NULL,
    connector           TEXT,
    extra_props         JSONB NOT NULL DEFAULT '{}'::jsonb,
    mode                TEXT NOT NULL CHECK (mode IN ('managed', 'adopted', 'native')),
    enabled             BOOLEAN NOT NULL DEFAULT true,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (engine_id, engine_catalog_name),
    UNIQUE (catalog_id, engine_id)
);

CREATE INDEX catalog_bindings_catalog_idx ON catalog_bindings (catalog_id);

-- Backfill 1: every Trino cluster becomes a system-owned trino connection
-- (created_by NULL) and a federated engine.
-- +goose StatementBegin
DO $baleia$
DECLARE
    tc        RECORD;
    conn_name TEXT;
    eng_name  TEXT;
    n         INT;
    conn_id   UUID;
    eng_id    UUID;
BEGIN
    FOR tc IN SELECT * FROM trino_clusters WHERE engine_id IS NULL ORDER BY created_at LOOP
        conn_name := 'trino-' || tc.name;
        n := 1;
        WHILE EXISTS (SELECT 1 FROM catalog_connections WHERE name = conn_name) LOOP
            n := n + 1;
            conn_name := 'trino-' || tc.name || '-' || n;
        END LOOP;

        eng_name := lower(regexp_replace(tc.name, '[^a-zA-Z0-9_-]', '_', 'g'));
        IF eng_name !~ '^[a-z]' THEN
            eng_name := 'trino_' || eng_name;
        END IF;
        eng_name := left(eng_name, 60);
        n := 1;
        WHILE EXISTS (SELECT 1 FROM engines WHERE name = eng_name) LOOP
            n := n + 1;
            eng_name := left(eng_name, 56) || '-' || n;
        END LOOP;

        INSERT INTO catalog_connections (name, type, url, config, created_by)
        VALUES (conn_name, 'trino', tc.coordinator_url, jsonb_build_object('url', tc.coordinator_url), NULL)
        RETURNING id INTO conn_id;

        INSERT INTO engines (name, kind, class, connection_id, identity_mode)
        VALUES (eng_name, 'trino', 'federated', conn_id, 'impersonation')
        RETURNING id INTO eng_id;

        UPDATE trino_clusters SET engine_id = eng_id WHERE id = tc.id;
    END LOOP;
END
$baleia$;
-- +goose StatementEnd

-- Backfill 2: every catalog is a source catalog with a managed binding on the
-- engine of its cluster.
UPDATE catalogs SET source_ref = warehouse WHERE origin = 'source';

INSERT INTO catalog_bindings (catalog_id, engine_id, engine_catalog_name, connector, extra_props, mode, enabled)
SELECT c.id, tc.engine_id, c.trino_catalog_name, c.connector, c.extra_props, 'managed', c.enabled
FROM catalogs c
JOIN trino_clusters tc ON tc.id = c.cluster_id;

-- Transitional: the catalog handlers still write the Trino columns of
-- catalogs. This trigger keeps the managed binding in step until those writes
-- move to catalog_bindings (#13 PR 2, #80), which drops it.
-- +goose StatementBegin
CREATE FUNCTION catalogs_set_source_ref() RETURNS trigger
LANGUAGE plpgsql AS $baleia$
BEGIN
    IF NEW.origin = 'source' THEN
        NEW.source_ref := NEW.warehouse;
    END IF;
    RETURN NEW;
END
$baleia$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION catalogs_upsert_managed_binding() RETURNS trigger
LANGUAGE plpgsql AS $baleia$
DECLARE
    eng UUID;
BEGIN
    IF NEW.origin <> 'source' THEN
        RETURN NULL;
    END IF;
    SELECT engine_id INTO eng FROM trino_clusters WHERE id = NEW.cluster_id;
    IF eng IS NULL THEN
        RETURN NULL;
    END IF;
    INSERT INTO catalog_bindings (catalog_id, engine_id, engine_catalog_name, connector, extra_props, mode, enabled)
    VALUES (NEW.id, eng, NEW.trino_catalog_name, NEW.connector, NEW.extra_props, 'managed', NEW.enabled)
    ON CONFLICT (catalog_id, engine_id) DO UPDATE SET
        engine_catalog_name = EXCLUDED.engine_catalog_name,
        connector           = EXCLUDED.connector,
        extra_props         = EXCLUDED.extra_props,
        enabled             = EXCLUDED.enabled,
        updated_at          = now();
    -- A move to another cluster leaves no binding behind on the old engine.
    DELETE FROM catalog_bindings
    WHERE catalog_id = NEW.id AND mode = 'managed' AND engine_id <> eng;
    RETURN NULL;
END
$baleia$;
-- +goose StatementEnd

CREATE TRIGGER catalogs_set_source_ref
    BEFORE INSERT OR UPDATE ON catalogs
    FOR EACH ROW EXECUTE FUNCTION catalogs_set_source_ref();

CREATE TRIGGER catalogs_upsert_managed_binding
    AFTER INSERT OR UPDATE ON catalogs
    FOR EACH ROW EXECUTE FUNCTION catalogs_upsert_managed_binding();

-- Execution targets: a workspace default and, per execution, the resolved
-- target plus an immutable snapshot (no secrets).
ALTER TABLE workspaces
    ADD COLUMN default_binding_id UUID REFERENCES catalog_bindings (id) ON DELETE SET NULL,
    ADD COLUMN default_schema     TEXT;

ALTER TABLE query_executions
    ADD COLUMN engine_id         UUID REFERENCES engines (id) ON DELETE SET NULL,
    ADD COLUMN target_binding_id UUID REFERENCES catalog_bindings (id) ON DELETE SET NULL,
    ADD COLUMN target_catalog_id UUID REFERENCES catalogs (id) ON DELETE SET NULL,
    ADD COLUMN target_schema     TEXT,
    ADD COLUMN target_snapshot   JSONB;

-- Backfill 3: past executions point at the default cluster's binding of the
-- catalog they ran against; rows whose catalog is gone keep a snapshot only.
UPDATE query_executions qe
SET target_binding_id = b.id,
    target_catalog_id = b.catalog_id,
    engine_id         = b.engine_id,
    target_snapshot   = jsonb_build_object(
        'engine_kind', 'trino', 'engine_name', e.name,
        'catalog_name', c.name, 'engine_catalog', qe.trino_catalog, 'schema', NULL)
FROM catalog_bindings b
JOIN engines e ON e.id = b.engine_id
JOIN trino_clusters tc ON tc.engine_id = e.id
JOIN catalogs c ON c.id = b.catalog_id
WHERE tc.name = 'default' AND b.engine_catalog_name = qe.trino_catalog;

UPDATE query_executions
SET target_snapshot = jsonb_build_object('engine_kind', 'trino', 'engine_catalog', trino_catalog)
WHERE target_snapshot IS NULL AND trino_catalog IS NOT NULL;

-- +goose Down

ALTER TABLE query_executions
    DROP COLUMN IF EXISTS target_snapshot,
    DROP COLUMN IF EXISTS target_schema,
    DROP COLUMN IF EXISTS target_catalog_id,
    DROP COLUMN IF EXISTS target_binding_id,
    DROP COLUMN IF EXISTS engine_id;

ALTER TABLE workspaces
    DROP COLUMN IF EXISTS default_schema,
    DROP COLUMN IF EXISTS default_binding_id;

DROP TRIGGER IF EXISTS catalogs_upsert_managed_binding ON catalogs;
DROP TRIGGER IF EXISTS catalogs_set_source_ref ON catalogs;
DROP FUNCTION IF EXISTS catalogs_upsert_managed_binding();
DROP FUNCTION IF EXISTS catalogs_set_source_ref();

DROP TABLE IF EXISTS catalog_bindings;

ALTER TABLE catalogs
    DROP COLUMN IF EXISTS source_ref,
    DROP COLUMN IF EXISTS origin;

ALTER TABLE trino_clusters DROP COLUMN IF EXISTS engine_id;

-- Direct engines' connections stay (users created them); only the backing
-- connections the Up migration created for Trino clusters go.
CREATE TEMP TABLE baleia_engine_connections ON COMMIT DROP AS
    SELECT connection_id FROM engines WHERE kind = 'trino';

DROP TABLE IF EXISTS engines;

DELETE FROM catalog_connections
WHERE type = 'trino' AND created_by IS NULL
  AND id IN (SELECT connection_id FROM baleia_engine_connections);
