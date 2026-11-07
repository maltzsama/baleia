-- Schema for sqlc, generated from internal/store/migrations (#759).
-- sqlc reads this until it is removed with the query migration (#761).

CREATE TABLE auth_providers (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT        NOT NULL,
    type        TEXT        NOT NULL CHECK (type IN ('oidc')),
    config_enc  TEXT          NOT NULL,
    enabled     BOOLEAN          NOT NULL DEFAULT true,
    created_at  TIMESTAMPTZ   NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ   NOT NULL DEFAULT now()
);

CREATE TABLE users (
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name                  TEXT        NOT NULL,
    email                 TEXT        NOT NULL UNIQUE,
    password_hash         TEXT,
    role                  TEXT        NOT NULL DEFAULT 'member' CHECK (role IN ('admin', 'member')),
    auth_provider         TEXT        NOT NULL DEFAULT 'local' CHECK (auth_provider IN ('local', 'oidc')),
    auth_provider_id      UUID          REFERENCES auth_providers (id) ON DELETE SET NULL,
    external_id           TEXT,
    personal_workspace_id UUID,
    created_at            TIMESTAMPTZ   NOT NULL DEFAULT now(),
    updated_at            TIMESTAMPTZ   NOT NULL DEFAULT now(),
    CONSTRAINT chk_local_auth  CHECK (auth_provider != 'local'  OR password_hash IS NOT NULL),
    CONSTRAINT chk_oidc_auth   CHECK (auth_provider != 'oidc'   OR (auth_provider_id IS NOT NULL AND external_id IS NOT NULL)),
    UNIQUE (auth_provider_id, external_id)
);

CREATE TABLE api_keys (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID          NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name        TEXT        NOT NULL,
    key_hash    TEXT        NOT NULL UNIQUE,
    expires_at  TIMESTAMPTZ,
    created_at  TIMESTAMPTZ   NOT NULL DEFAULT now()
);

CREATE INDEX idx_users_email          ON users (email);
CREATE INDEX idx_users_external_id    ON users (auth_provider_id, external_id);
CREATE INDEX idx_api_keys_user_id     ON api_keys (user_id);
CREATE INDEX idx_api_keys_key_hash    ON api_keys (key_hash);

CREATE TABLE teams (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT        NOT NULL,
    description TEXT,
    created_by  UUID          NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    created_at  TIMESTAMPTZ   NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ   NOT NULL DEFAULT now()
);

CREATE TABLE team_members (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    team_id     UUID          NOT NULL REFERENCES teams (id) ON DELETE CASCADE,
    user_id     UUID          NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    role        TEXT        NOT NULL DEFAULT 'member' CHECK (role IN ('owner', 'member')),
    joined_at   TIMESTAMPTZ   NOT NULL DEFAULT now(),
    UNIQUE (team_id, user_id)
);

CREATE TABLE workspaces (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name                 TEXT        NOT NULL,
    description          TEXT,
    type                 TEXT        NOT NULL CHECK (type IN ('personal', 'shared')),
    polaris_catalog_name TEXT        NOT NULL UNIQUE,
    owner_user_id        UUID          REFERENCES users (id) ON DELETE RESTRICT,
    owner_team_id        UUID          REFERENCES teams (id) ON DELETE RESTRICT,
    created_at           TIMESTAMPTZ   NOT NULL DEFAULT now(),
    updated_at           TIMESTAMPTZ   NOT NULL DEFAULT now(),
    CONSTRAINT chk_workspace_owner CHECK (
        (type = 'personal' AND owner_user_id IS NOT NULL AND owner_team_id IS NULL) OR
        (type = 'shared'   AND owner_team_id IS NOT NULL AND owner_user_id IS NULL)
    )
);

ALTER TABLE users
    ADD CONSTRAINT fk_personal_workspace
    FOREIGN KEY (personal_workspace_id)
    REFERENCES workspaces (id)
    ON DELETE SET NULL;

CREATE INDEX idx_teams_created_by       ON teams (created_by);
CREATE INDEX idx_team_members_team_id   ON team_members (team_id);
CREATE INDEX idx_team_members_user_id   ON team_members (user_id);
CREATE INDEX idx_workspaces_owner_user  ON workspaces (owner_user_id);
CREATE INDEX idx_workspaces_owner_team  ON workspaces (owner_team_id);

CREATE TABLE catalog_connections (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name             TEXT        NOT NULL UNIQUE,
    type             TEXT        NOT NULL CHECK (type IN ('polaris', 'nessie', 'glue', 'hive')),
    url              TEXT        NOT NULL,
    credentials_enc  TEXT,
    default_readonly BOOLEAN          NOT NULL DEFAULT true,
    created_at       TIMESTAMPTZ   NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ   NOT NULL DEFAULT now()
);

CREATE TABLE workspace_catalog_access (
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id          UUID          NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    catalog_connection_id UUID          NOT NULL REFERENCES catalog_connections (id) ON DELETE CASCADE,
    permission            TEXT        NOT NULL CHECK (permission IN ('read', 'write')),
    granted_by            UUID          NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    granted_at            TIMESTAMPTZ   NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, catalog_connection_id)
);

CREATE INDEX idx_workspace_catalog_access_workspace ON workspace_catalog_access (workspace_id);
CREATE INDEX idx_workspace_catalog_access_catalog   ON workspace_catalog_access (catalog_connection_id);

CREATE TABLE query_folders (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID          NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    parent_id    UUID          REFERENCES query_folders (id) ON DELETE CASCADE,
    name         TEXT        NOT NULL,
    created_at   TIMESTAMPTZ   NOT NULL DEFAULT now()
);

CREATE TABLE saved_queries (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID          NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    folder_id    UUID          REFERENCES query_folders (id) ON DELETE SET NULL,
    created_by   UUID          NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    name         TEXT        NOT NULL,
    sql          TEXT          NOT NULL,
    created_at   TIMESTAMPTZ   NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ   NOT NULL DEFAULT now()
);

CREATE TABLE query_executions (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id    UUID          NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    user_id         UUID          NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    saved_query_id  UUID          REFERENCES saved_queries (id) ON DELETE SET NULL,
    status          TEXT        NOT NULL CHECK (status IN ('running', 'finished', 'failed', 'cancelled')),
    sql             TEXT          NOT NULL,
    trino_query_id  TEXT,
    rows_returned   BIGINT,
    duration_ms     BIGINT,
    started_at      TIMESTAMPTZ   NOT NULL DEFAULT now(),
    finished_at     TIMESTAMPTZ
);

CREATE TABLE query_statements (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    execution_id    UUID          NOT NULL REFERENCES query_executions (id) ON DELETE CASCADE,
    position        INTEGER          NOT NULL,
    sql             TEXT          NOT NULL,
    status          TEXT        NOT NULL CHECK (status IN ('running', 'finished', 'failed', 'cancelled')),
    trino_query_id  TEXT,
    rows_returned   BIGINT,
    duration_ms     BIGINT,
    error_message   TEXT,
    started_at      TIMESTAMPTZ   NOT NULL DEFAULT now(),
    finished_at     TIMESTAMPTZ
);

CREATE INDEX idx_query_folders_workspace    ON query_folders (workspace_id);
CREATE INDEX idx_query_folders_parent       ON query_folders (parent_id);
CREATE INDEX idx_saved_queries_workspace    ON saved_queries (workspace_id);
CREATE INDEX idx_saved_queries_folder       ON saved_queries (folder_id);
CREATE INDEX idx_saved_queries_created_by   ON saved_queries (created_by);
CREATE INDEX idx_query_executions_workspace ON query_executions (workspace_id);
CREATE INDEX idx_query_executions_user      ON query_executions (user_id);
CREATE INDEX idx_query_executions_status    ON query_executions (status);
CREATE INDEX idx_query_statements_execution ON query_statements (execution_id);

CREATE TABLE retrievers (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id     UUID          NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    created_by       UUID          NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    name             TEXT        NOT NULL,
    definition_sql   TEXT          NOT NULL,
    dest_catalog     TEXT        NOT NULL,
    dest_namespace   TEXT        NOT NULL,
    dest_table       TEXT        NOT NULL,
    cron_schedule    TEXT        NOT NULL,
    refresh_type     TEXT        NOT NULL DEFAULT 'incremental' CHECK (refresh_type IN ('incremental', 'full')),
    status           TEXT        NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'paused', 'error')),
    last_refresh_at  TIMESTAMPTZ,
    next_refresh_at  TIMESTAMPTZ,
    created_at       TIMESTAMPTZ   NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ   NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, name)
);

CREATE TABLE retriever_refresh_history (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    retriever_id         UUID          NOT NULL REFERENCES retrievers (id) ON DELETE CASCADE,
    temporal_workflow_id TEXT        NOT NULL,
    refresh_type         TEXT        NOT NULL CHECK (refresh_type IN ('incremental', 'full')),
    status               TEXT        NOT NULL CHECK (status IN ('running', 'finished', 'failed')),
    rows_processed       BIGINT,
    duration_ms          BIGINT,
    error_message        TEXT,
    started_at           TIMESTAMPTZ   NOT NULL DEFAULT now(),
    finished_at          TIMESTAMPTZ
);

CREATE INDEX idx_retrievers_workspace         ON retrievers (workspace_id);
CREATE INDEX idx_retrievers_status            ON retrievers (status);
CREATE INDEX idx_retrievers_next_refresh_at   ON retrievers (next_refresh_at);
CREATE INDEX idx_retriever_refresh_retriever  ON retriever_refresh_history (retriever_id);
CREATE INDEX idx_retriever_refresh_status     ON retriever_refresh_history (status);

CREATE TABLE migrations (
    id                          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    created_by                  UUID          NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    name                        TEXT        NOT NULL,
    source_type                 TEXT        NOT NULL CHECK (source_type IN ('nessie')),
    source_url                  TEXT        NOT NULL,
    source_credentials_enc      TEXT,
    target_catalog_connection_id UUID         NOT NULL REFERENCES catalog_connections (id) ON DELETE RESTRICT,
    status                      TEXT        NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'running', 'finished', 'failed', 'cancelled')),
    total_tables                INTEGER          NOT NULL DEFAULT 0,
    migrated_tables             INTEGER          NOT NULL DEFAULT 0,
    failed_tables               INTEGER          NOT NULL DEFAULT 0,
    temporal_workflow_id        TEXT,
    started_at                  TIMESTAMPTZ,
    finished_at                 TIMESTAMPTZ,
    created_at                  TIMESTAMPTZ   NOT NULL DEFAULT now()
);

CREATE TABLE migration_items (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    migration_id      UUID          NOT NULL REFERENCES migrations (id) ON DELETE CASCADE,
    source_namespace  TEXT        NOT NULL,
    source_table      TEXT        NOT NULL,
    target_namespace  TEXT        NOT NULL,
    target_table      TEXT        NOT NULL,
    status            TEXT        NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'running', 'finished', 'failed', 'skipped')),
    error_message     TEXT,
    started_at        TIMESTAMPTZ,
    finished_at       TIMESTAMPTZ
);

CREATE INDEX idx_migrations_created_by      ON migrations (created_by);
CREATE INDEX idx_migrations_status          ON migrations (status);
CREATE INDEX idx_migration_items_migration  ON migration_items (migration_id);
CREATE INDEX idx_migration_items_status     ON migration_items (status);

ALTER TABLE users
    ADD COLUMN password_reset_token      TEXT,
    ADD COLUMN password_reset_expires_at TIMESTAMPTZ;

CREATE TABLE refresh_tokens (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash TEXT NOT NULL UNIQUE,
    expires_at TIMESTAMPTZ NOT NULL,
    revoked    BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_refresh_tokens_user_id ON refresh_tokens(user_id);
CREATE INDEX idx_refresh_tokens_expires_at ON refresh_tokens(expires_at);

ALTER TABLE query_executions
    ADD COLUMN trino_catalog TEXT,
    ADD COLUMN result_key  TEXT;

CREATE TABLE trino_query_snapshots (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    trino_query_id       TEXT NOT NULL UNIQUE,
    execution_id         UUID REFERENCES query_executions(id) ON DELETE SET NULL,
    origin               TEXT NOT NULL CHECK (origin IN ('baleia', 'trino_direct')),
    "user"               TEXT NOT NULL,
    state                TEXT NOT NULL CHECK (state IN ('running', 'finished', 'failed', 'cancelled')),
    sql                  TEXT NOT NULL,
    catalog              TEXT,
    "schema"             TEXT,
    created_at           TIMESTAMPTZ NOT NULL,
    elapsed_ms           INTEGER,
    cpu_ms               INTEGER,
    physical_input_bytes BIGINT,
    output_rows          BIGINT,
    error_message        TEXT,
    raw_trino_payload    JSONB,
    first_seen_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_trino_query_snapshots_query_id ON trino_query_snapshots(trino_query_id);
CREATE INDEX idx_trino_query_snapshots_origin   ON trino_query_snapshots(origin);
CREATE INDEX idx_trino_query_snapshots_state    ON trino_query_snapshots(state);
CREATE INDEX idx_trino_query_snapshots_user     ON trino_query_snapshots("user");
CREATE INDEX idx_trino_query_snapshots_created  ON trino_query_snapshots(created_at);

CREATE TABLE cluster_snapshots (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    captured_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    active_workers   INTEGER NOT NULL DEFAULT 0,
    running_queries  INTEGER NOT NULL DEFAULT 0,
    queued_queries   INTEGER NOT NULL DEFAULT 0,
    blocked_queries  INTEGER NOT NULL DEFAULT 0,
    total_memory_bytes  BIGINT,
    free_memory_bytes   BIGINT,
    raw_payload      JSONB
);

CREATE INDEX idx_cluster_snapshots_captured ON cluster_snapshots(captured_at);

CREATE TABLE table_health_snapshots (
    id                     UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    catalog_connection_id  UUID NOT NULL REFERENCES catalog_connections(id) ON DELETE CASCADE,
    catalog                TEXT NOT NULL,
    namespace              TEXT NOT NULL,
    table_name             TEXT NOT NULL,
    captured_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    snapshot_count         INTEGER NOT NULL DEFAULT 0,
    file_count             INTEGER,
    total_size_bytes       BIGINT,
    oldest_snapshot_age_hours INTEGER,
    last_maintenance_at    TIMESTAMPTZ,
    health_score           INTEGER NOT NULL DEFAULT 100 CHECK (health_score >= 0 AND health_score <= 100),
    needs_compaction       BOOLEAN NOT NULL DEFAULT false,
    needs_snapshot_expiry  BOOLEAN NOT NULL DEFAULT false,
    needs_orphan_cleanup   BOOLEAN NOT NULL DEFAULT false,
    UNIQUE(catalog_connection_id, catalog, namespace, table_name)
);

CREATE INDEX idx_table_health_conn     ON table_health_snapshots(catalog_connection_id);
CREATE INDEX idx_table_health_captured ON table_health_snapshots(captured_at);

CREATE TABLE catalogs (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    connection_id      UUID NOT NULL REFERENCES catalog_connections(id) ON DELETE CASCADE,
    name               TEXT NOT NULL,
    description        TEXT,
    trino_catalog_name TEXT NOT NULL,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (connection_id, name),
    UNIQUE (trino_catalog_name)
);

DROP TABLE IF EXISTS workspace_catalog_access;

CREATE TABLE workspace_catalog_access (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    catalog_id   UUID NOT NULL REFERENCES catalogs(id) ON DELETE CASCADE,
    permission   TEXT NOT NULL DEFAULT 'read',
    granted_by   UUID NOT NULL REFERENCES users(id),
    granted_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, catalog_id)
);

ALTER TABLE catalog_connections
    DROP CONSTRAINT catalog_connections_type_check;

ALTER TABLE catalog_connections
    ADD CONSTRAINT catalog_connections_type_check
    CHECK (type IN ('polaris', 'nessie', 'glue', 'hive', 'iceberg', 'postgresql', 'mysql'));

ALTER TABLE catalog_connections
    ADD COLUMN host TEXT,
    ADD COLUMN port TEXT;

ALTER TABLE catalogs
    DROP COLUMN trino_catalog_name;

ALTER TABLE catalog_connections ADD COLUMN created_by UUID REFERENCES users(id) ON DELETE SET NULL;

DELETE FROM saved_queries sq1 USING saved_queries sq2
WHERE sq1.created_at < sq2.created_at
  AND sq1.workspace_id = sq2.workspace_id
  AND sq1.name = sq2.name;

CREATE UNIQUE INDEX idx_saved_queries_unique_name ON saved_queries (workspace_id, name);

DROP TABLE IF EXISTS workspace_catalog_access;
DROP TABLE IF EXISTS catalogs;

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
DO $baleia$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'baleia_trino') THEN
        CREATE ROLE baleia_trino LOGIN;
    END IF;
END
$baleia$;

GRANT USAGE ON SCHEMA public TO baleia_trino;
GRANT SELECT, INSERT, UPDATE, DELETE ON trino_catalog_registry TO baleia_trino;
GRANT SELECT ON trino_clusters TO baleia_trino;

-- The baleia-trino-catalog-store plugin (v0.3.0+) reserves "env" and "file" as
-- catalog names because they collide with the @baleia-secret[<origin>:<key>]
-- origin schemes. Extend the CHECK to match the plugin's CatalogRow validation.
ALTER TABLE catalogs
    DROP CONSTRAINT catalogs_trino_name_not_reserved;

ALTER TABLE catalogs
    ADD CONSTRAINT catalogs_trino_name_not_reserved
    CHECK (trino_catalog_name NOT IN ('system', 'jmx', 'tpch', 'tpcds', 'memory', 'env', 'file'));

-- Correlation: keep the HTTP X-Request-ID on the execution row so a query can
-- be traced from the request log through the Temporal workflow and back.
ALTER TABLE query_executions ADD COLUMN request_id TEXT;

ALTER TABLE query_statements
    ADD COLUMN result_key   TEXT,
    ADD COLUMN result_bytes BIGINT,
    ADD COLUMN truncated    BOOLEAN NOT NULL DEFAULT false;

-- Grants of a catalog to a workspace. The previous table (workspace_catalog_access,
-- keyed by connection) was dropped in 00017, so there is nothing to carry over.
CREATE TABLE workspace_catalog_grants (
    workspace_id UUID        NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    catalog_id   UUID        NOT NULL REFERENCES catalogs (id)   ON DELETE CASCADE,
    permission   TEXT        NOT NULL CHECK (permission IN ('read', 'write')),
    granted_by   UUID        NOT NULL REFERENCES users (id)      ON DELETE RESTRICT,
    granted_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, catalog_id)
);

CREATE INDEX idx_workspace_catalog_grants_catalog ON workspace_catalog_grants (catalog_id);

-- Every kind the provider registry may offer (#11). The API accepts only the
-- kinds that have a registered provider; the CHECK already allows the rest.
ALTER TABLE catalog_connections
    DROP CONSTRAINT catalog_connections_type_check;

ALTER TABLE catalog_connections
    ADD CONSTRAINT catalog_connections_type_check
    CHECK (type IN (
        'polaris', 'nessie', 'iceberg', 'lakekeeper', 'unity', 'hive', 'glue',
        'postgresql', 'mysql', 'mariadb', 'sqlserver', 'oracle', 'sqlite', 'clickhouse',
        'flightsql', 'trino', 'doris', 'starrocks'));

-- config holds the non-secret values of the provider's ConfigSchema; secrets
-- stay sealed in credentials_enc. credentials_read_only is an admin assertion
-- that the stored credentials cannot write (docs/adr/0011, #20).
ALTER TABLE catalog_connections
    ADD COLUMN config JSONB NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN credentials_read_only BOOLEAN NOT NULL DEFAULT false;

-- Backfill from the columns config replaces. The application converts what
-- remains in the url (a JDBC URL for databases) and in the credentials blob.
-- url/host/port and default_readonly are kept for one release and dropped in
-- a follow-up.
UPDATE catalog_connections
SET config = jsonb_strip_nulls(jsonb_build_object('url', url, 'host', host, 'port', port));

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
CREATE FUNCTION catalogs_set_source_ref() RETURNS trigger
LANGUAGE plpgsql AS $baleia$
BEGIN
    IF NEW.origin = 'source' THEN
        NEW.source_ref := NEW.warehouse;
    END IF;
    RETURN NEW;
END
$baleia$;
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

-- Native catalogs (found inside an engine, #13) have no Trino projection, so
-- the Trino columns of catalogs become nullable. Source catalogs keep them:
-- the CHECK holds what NOT NULL held for every row before.
ALTER TABLE catalogs
    ALTER COLUMN trino_catalog_name DROP NOT NULL,
    ALTER COLUMN cluster_id DROP NOT NULL,
    ALTER COLUMN warehouse DROP NOT NULL,
    ALTER COLUMN connector DROP NOT NULL,
    ADD CONSTRAINT catalogs_source_has_trino_columns CHECK (
        origin = 'native'
        OR (trino_catalog_name IS NOT NULL AND cluster_id IS NOT NULL
            AND warehouse IS NOT NULL AND connector IS NOT NULL));

-- Engine-neutral vocabulary (#20): a statement's id on its engine is not a
-- Trino id once direct engines run statements. Lens keeps
-- trino_query_snapshots.trino_query_id: it monitors Trino.
ALTER TABLE query_executions RENAME COLUMN trino_query_id TO engine_query_id;
ALTER TABLE query_statements RENAME COLUMN trino_query_id TO engine_query_id;

-- query_handle is the engine's QueryHandle (no credentials), persisted as soon
-- as the engine assigns it so another process can cancel the statement.
-- result_sets lists the stored results of the statement, one per result set:
-- [{result_index, result_key, rows, bytes, truncated}].
ALTER TABLE query_statements
    ADD COLUMN query_handle JSONB,
    ADD COLUMN result_sets JSONB NOT NULL DEFAULT '[]'::jsonb;

UPDATE query_statements
SET result_sets = jsonb_build_array(jsonb_build_object(
        'result_index', 0, 'result_key', result_key,
        'rows', coalesce(rows_returned, 0), 'bytes', coalesce(result_bytes, 0), 'truncated', truncated))
WHERE result_key IS NOT NULL;

-- The catalog handlers and the binding API write catalog_bindings
-- themselves now (#80): the transitional triggers of 00024 go, and
-- source_ref is written with the row.
DROP TRIGGER IF EXISTS catalogs_upsert_managed_binding ON catalogs;
DROP TRIGGER IF EXISTS catalogs_set_source_ref ON catalogs;
DROP FUNCTION IF EXISTS catalogs_upsert_managed_binding();
DROP FUNCTION IF EXISTS catalogs_set_source_ref();

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

-- The registry refuses the names Trino and the plugin reserve (#39), as the
-- catalogs table does since 00019; contract/reserved_catalog_names.json of
-- the pinned plugin release is the list, checked by internal/contract.
-- Added NOT VALID and validated in its own statement (no transaction), so the
-- scan of existing rows does not hold the ACCESS EXCLUSIVE lock; the DROP
-- makes a rerun after a failed validation start over.
ALTER TABLE trino_catalog_registry DROP CONSTRAINT IF EXISTS registry_catalog_name_not_reserved;
ALTER TABLE trino_catalog_registry
    ADD CONSTRAINT registry_catalog_name_not_reserved
        CHECK (catalog_name NOT IN ('system', 'jmx', 'tpch', 'tpcds', 'memory', 'env', 'file')) NOT VALID;
ALTER TABLE trino_catalog_registry VALIDATE CONSTRAINT registry_catalog_name_not_reserved;

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

-- One activity runs every statement of an execution in one engine session
-- (#27): statements are created 'pending' and become 'running' one at a time;
-- after a failure, a cancel or a crash the rest are 'skipped'. error_code is
-- the typed engine code of a failed statement (queryengine.ErrorCode, or
-- 'interrupted' when the worker died mid-statement).
ALTER TABLE query_statements DROP CONSTRAINT query_statements_status_check;
ALTER TABLE query_statements ADD CONSTRAINT query_statements_status_check
    CHECK (status IN ('pending', 'running', 'finished', 'failed', 'cancelled', 'skipped'));
ALTER TABLE query_statements ADD COLUMN error_code TEXT;

-- Physical metadata snapshots (#15): namespaces, tables and columns per
-- logical catalog, filled by the metadata sync. Keyed by catalog, never by
-- workspace: grants scope them at read time (#18). Rows not seen by a pass
-- are tombstoned (removed_at), not deleted; the sync purges tombstones
-- older than 7 days.

-- pg_trgm is a trusted extension: CREATE on the database is enough.
CREATE EXTENSION IF NOT EXISTS pg_trgm;

CREATE TABLE metadata_namespaces (
    catalog_id    UUID        NOT NULL REFERENCES catalogs (id) ON DELETE CASCADE,
    namespace     TEXT[]      NOT NULL,
    last_seen_at  TIMESTAMPTZ NOT NULL,
    removed_at    TIMESTAMPTZ,
    PRIMARY KEY (catalog_id, namespace)
);

CREATE TABLE metadata_tables (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    catalog_id       UUID        NOT NULL REFERENCES catalogs (id) ON DELETE CASCADE,
    namespace        TEXT[]      NOT NULL,
    name             TEXT        NOT NULL,
    kind             TEXT        NOT NULL CHECK (kind IN ('table', 'view', 'materialized_view')),
    -- Iceberg: the metadata-location; databases and engines: md5 of the
    -- ordered column list. A changed fingerprint reloads the columns.
    fingerprint      TEXT        NOT NULL,
    -- format, location, current_snapshot_id, etag, … (what the source knows)
    properties       JSONB       NOT NULL DEFAULT '{}'::jsonb,
    comment          TEXT,
    last_seen_at     TIMESTAMPTZ NOT NULL,
    last_changed_at  TIMESTAMPTZ NOT NULL,
    removed_at       TIMESTAMPTZ,
    UNIQUE (catalog_id, namespace, name)
);
CREATE INDEX metadata_tables_name_trgm ON metadata_tables USING gin (name gin_trgm_ops) WHERE removed_at IS NULL;

CREATE TABLE metadata_columns (
    table_id     UUID    NOT NULL REFERENCES metadata_tables (id) ON DELETE CASCADE,
    ordinal      INT     NOT NULL,
    name         TEXT    NOT NULL,
    -- The type as the source reports it, in its own type system.
    native_type  TEXT    NOT NULL,
    type_system  TEXT    NOT NULL,
    nullable     BOOLEAN,
    comment      TEXT,
    PRIMARY KEY (table_id, ordinal)
);
CREATE INDEX metadata_columns_name_trgm ON metadata_columns USING gin (name gin_trgm_ops);

CREATE TABLE metadata_sync_runs (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    catalog_id      UUID        NOT NULL REFERENCES catalogs (id) ON DELETE CASCADE,
    started_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at     TIMESTAMPTZ,
    status          TEXT        NOT NULL CHECK (status IN ('running', 'finished', 'failed')),
    tables_seen     INT,
    tables_changed  INT,
    tables_removed  INT,
    error_code      TEXT,
    error_message   TEXT,
    -- provider: the catalog's own source (catalog manager, database driver);
    -- engine: introspection through one of the catalog's bindings.
    source_path     TEXT        NOT NULL CHECK (source_path IN ('provider', 'engine'))
);
CREATE INDEX metadata_sync_runs_catalog ON metadata_sync_runs (catalog_id, started_at DESC);

-- Append-only audit trail (#45). The application connects as the role that
-- owns the table, and an owner keeps every privilege whatever REVOKE says:
-- append-only is enforced by a trigger. Partitioned by month; there is no
-- DEFAULT partition (rows in it would make creating the matching range
-- partition fail later), so a missing partition makes Record fail loudly.
CREATE TABLE audit_events (
    id            BIGINT GENERATED ALWAYS AS IDENTITY,
    occurred_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    actor_kind    TEXT        NOT NULL CHECK (actor_kind IN ('user', 'service', 'agent', 'anonymous')),
    actor_id      TEXT,
    auth_method   TEXT,
    -- Bounded vocabulary, internal/audit/actions.go: <resource>.<verb>.
    action        TEXT        NOT NULL CHECK (action ~ '^[a-z_]+\.[a-z_]+$'),
    resource_type TEXT        NOT NULL,
    resource_id   TEXT,
    workspace_id  UUID,
    outcome       TEXT        NOT NULL CHECK (outcome IN ('allowed', 'denied', 'error')),
    reason        TEXT,
    request_id    TEXT,
    trace_id      TEXT,
    client_ip     INET,
    user_agent    TEXT,
    -- {"field": {"from": …, "to": …}}; secret values are stored as "***".
    changes       JSONB,
    PRIMARY KEY (occurred_at, id)
) PARTITION BY RANGE (occurred_at);

CREATE INDEX audit_events_resource ON audit_events (resource_type, resource_id, occurred_at DESC);
CREATE INDEX audit_events_actor    ON audit_events (actor_id, occurred_at DESC);
CREATE INDEX audit_events_ws       ON audit_events (workspace_id, occurred_at DESC);

-- audit_ensure_partitions creates the monthly partitions of the current
-- month and the next months_ahead ones, if missing. It never drops one: the
-- worker calls it daily (schedule audit-partitions); retention is the
-- explicit operator command `migrate -cmd=audit-prune`.
CREATE FUNCTION audit_ensure_partitions(months_ahead int) RETURNS void LANGUAGE plpgsql AS $$
DECLARE
    m date := date_trunc('month', now())::date;
BEGIN
    FOR i IN 0..months_ahead LOOP
        EXECUTE format('CREATE TABLE IF NOT EXISTS %I PARTITION OF audit_events FOR VALUES FROM (%L) TO (%L)',
                       'audit_events_' || to_char(m + make_interval(months => i), 'YYYYMM'),
                       m + make_interval(months => i), m + make_interval(months => i + 1));
    END LOOP;
END $$;

SELECT audit_ensure_partitions(2);
CREATE FUNCTION audit_events_append_only() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'audit_events is append-only (% blocked)', TG_OP;
END $$;

CREATE TRIGGER audit_events_no_update BEFORE UPDATE OR DELETE ON audit_events
    FOR EACH ROW EXECUTE FUNCTION audit_events_append_only();

-- Binding reconciliation runs per federated engine (#37). Only the last 100
-- runs of an engine are kept: each insert prunes the older ones.
CREATE TABLE catalog_reconcile_runs (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    engine_id    UUID        NOT NULL REFERENCES engines (id) ON DELETE CASCADE,
    started_at   TIMESTAMPTZ NOT NULL,
    finished_at  TIMESTAMPTZ NOT NULL,
    dry_run      BOOLEAN     NOT NULL,
    -- [{catalog, kind, reason, result, error_code}]
    actions      JSONB       NOT NULL DEFAULT '[]'::jsonb,
    adopted      TEXT[]      NOT NULL DEFAULT '{}',
    error        TEXT
);
CREATE INDEX catalog_reconcile_runs_engine ON catalog_reconcile_runs (engine_id, started_at DESC);

-- Connection tests bind a short-lived catalog into Trino (#12). Its registry
-- row expires: the test drops it and hard-deletes the row (it holds real
-- credentials), and the ephemeral-catalog-sweeper does the same for rows a
-- crashed test left behind. NULL = a permanent catalog. The plugin selects
-- explicit columns, so it ignores this one.
ALTER TABLE trino_catalog_registry ADD COLUMN expires_at TIMESTAMPTZ;
CREATE INDEX trino_catalog_registry_expires ON trino_catalog_registry (expires_at) WHERE expires_at IS NOT NULL;

-- Test catalogs the old test path left behind when it failed midway: the
-- sweeper drops them on its first run.
UPDATE trino_catalog_registry SET expires_at = now() WHERE catalog_name LIKE 'baleia\_test\_%';

-- Export cursor for the audit trail (#50): the position (occurred_at, id) of
-- the last event a sink has accepted. One row per sink ('opensearch'). The
-- cursor only ever moves forward, and never past a failed item, so the export
-- is at-least-once, ordered and resumable. audit_events is never modified.
CREATE TABLE audit_export_cursor (
    sink        TEXT PRIMARY KEY,
    occurred_at TIMESTAMPTZ NOT NULL,
    id          BIGINT      NOT NULL,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

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

-- Spaces (#40, ADR 0013): named containers inside a workspace with their own
-- access list, owning folders, saved queries and semantic metrics.
--
-- A workspace always has exactly one default space (is_default), created with
-- the workspace. Today's behaviour is preserved: every member may edit content
-- in the default space (default_role = 'editor'). Semantic docs stay
-- workspace-scoped: they describe tables, not content.

CREATE TABLE spaces (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id  UUID NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    name          TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 120),
    description   TEXT,
    visibility    TEXT NOT NULL CHECK (visibility IN ('workspace', 'restricted')),
    default_role  TEXT NOT NULL DEFAULT 'viewer' CHECK (default_role IN ('viewer', 'editor')),
    is_default    BOOLEAN NOT NULL DEFAULT false,
    created_by    UUID REFERENCES users (id) ON DELETE SET NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, name),
    UNIQUE (workspace_id, id),            -- target of the composite FKs below
    CHECK (NOT is_default OR visibility = 'workspace')
);
CREATE UNIQUE INDEX spaces_one_default ON spaces (workspace_id) WHERE is_default;

CREATE TABLE space_members (
    space_id        UUID NOT NULL REFERENCES spaces (id) ON DELETE CASCADE,
    principal_type  TEXT NOT NULL CHECK (principal_type IN ('user', 'team')),
    principal_id    UUID NOT NULL,       -- users.id or teams.id; existence is checked by the handler
    role            TEXT NOT NULL CHECK (role IN ('viewer', 'editor', 'manager')),
    granted_by      UUID REFERENCES users (id) ON DELETE SET NULL,
    granted_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (space_id, principal_type, principal_id)
);
CREATE INDEX space_members_principal ON space_members (principal_type, principal_id);

-- Backfill: one default space per existing workspace, editable by every member
-- (exactly today's behaviour).
INSERT INTO spaces (workspace_id, name, visibility, default_role, is_default)
SELECT id, 'General', 'workspace', 'editor', true FROM workspaces;

-- Clean up references that crossed workspaces (possible through the old API).
UPDATE query_folders c SET parent_id = NULL
  FROM query_folders p WHERE c.parent_id = p.id AND p.workspace_id <> c.workspace_id;
UPDATE saved_queries q SET folder_id = NULL
  FROM query_folders f WHERE q.folder_id = f.id AND f.workspace_id <> q.workspace_id;

-- Folders and saved queries move into spaces. The composite FKs make
-- cross-space (and therefore cross-workspace) references impossible.
ALTER TABLE query_folders ADD COLUMN space_id UUID;
UPDATE query_folders f SET space_id = s.id
  FROM spaces s WHERE s.workspace_id = f.workspace_id AND s.is_default;

ALTER TABLE saved_queries
    ADD COLUMN space_id UUID,
    ADD COLUMN updated_by UUID REFERENCES users (id) ON DELETE SET NULL;
UPDATE saved_queries q SET space_id = s.id
  FROM spaces s WHERE s.workspace_id = q.workspace_id AND s.is_default;

ALTER TABLE query_folders
    DROP CONSTRAINT query_folders_parent_id_fkey,
    ALTER COLUMN space_id SET NOT NULL,
    ADD CONSTRAINT query_folders_space_fk FOREIGN KEY (workspace_id, space_id)
        REFERENCES spaces (workspace_id, id) ON DELETE CASCADE,
    ADD CONSTRAINT query_folders_space_id_uq UNIQUE (space_id, id);
-- Separate statement: the FK below needs the unique constraint above to exist.
ALTER TABLE query_folders
    ADD CONSTRAINT query_folders_parent_same_space FOREIGN KEY (space_id, parent_id)
        REFERENCES query_folders (space_id, id) ON DELETE CASCADE
        DEFERRABLE INITIALLY IMMEDIATE;

ALTER TABLE saved_queries
    DROP CONSTRAINT saved_queries_folder_id_fkey,
    ALTER COLUMN space_id SET NOT NULL,
    ADD CONSTRAINT saved_queries_space_fk FOREIGN KEY (workspace_id, space_id)
        REFERENCES spaces (workspace_id, id) ON DELETE CASCADE,
    ADD CONSTRAINT saved_queries_folder_same_space FOREIGN KEY (space_id, folder_id)
        REFERENCES query_folders (space_id, id) ON DELETE SET NULL (folder_id)
        DEFERRABLE INITIALLY IMMEDIATE;
CREATE INDEX saved_queries_space ON saved_queries (space_id);
CREATE INDEX query_folders_space ON query_folders (space_id);

-- Saved queries remember where they run (#13): opening one restores its target.
ALTER TABLE saved_queries
    ADD COLUMN target_binding_id UUID REFERENCES catalog_bindings (id) ON DELETE SET NULL,
    ADD COLUMN target_schema TEXT;

-- Portable identity (ADR 0013, used by #34 bundles).
ALTER TABLE saved_queries ADD COLUMN slug TEXT;
ALTER TABLE query_folders ADD COLUMN slug TEXT;
DO $baleia$
DECLARE
    r         RECORD;
    base      TEXT;
    candidate TEXT;
    n         INT;
BEGIN
    FOR r IN SELECT id, workspace_id, name FROM saved_queries ORDER BY created_at, id LOOP
        base := btrim(regexp_replace(lower(r.name), '[^a-z0-9]+', '_', 'g'), '_');
        IF base = '' THEN base := 'q'; END IF;
        IF base !~ '^[a-z]' THEN base := 'q_' || base; END IF;
        base := left(base, 63);
        candidate := base;
        n := 1;
        WHILE EXISTS (SELECT 1 FROM saved_queries WHERE workspace_id = r.workspace_id AND slug = candidate AND id <> r.id) LOOP
            n := n + 1;
            candidate := left(base, 63 - length('_' || n::text)) || '_' || n::text;
        END LOOP;
        UPDATE saved_queries SET slug = candidate WHERE id = r.id;
    END LOOP;

    FOR r IN SELECT id, space_id, name FROM query_folders ORDER BY created_at, id LOOP
        base := btrim(regexp_replace(lower(r.name), '[^a-z0-9]+', '_', 'g'), '_');
        IF base = '' THEN base := 'q'; END IF;
        IF base !~ '^[a-z]' THEN base := 'q_' || base; END IF;
        base := left(base, 63);
        candidate := base;
        n := 1;
        WHILE EXISTS (SELECT 1 FROM query_folders WHERE space_id = r.space_id AND slug = candidate AND id <> r.id) LOOP
            n := n + 1;
            candidate := left(base, 63 - length('_' || n::text)) || '_' || n::text;
        END LOOP;
        UPDATE query_folders SET slug = candidate WHERE id = r.id;
    END LOOP;
END $baleia$;

ALTER TABLE saved_queries
    ALTER COLUMN slug SET NOT NULL,
    ADD CONSTRAINT saved_queries_slug_format CHECK (slug ~ '^[a-z][a-z0-9_]{0,62}$'),
    ADD CONSTRAINT saved_queries_slug_uq UNIQUE (workspace_id, slug);
ALTER TABLE query_folders
    ALTER COLUMN slug SET NOT NULL,
    ADD CONSTRAINT query_folders_slug_format CHECK (slug ~ '^[a-z][a-z0-9_]{0,62}$'),
    ADD CONSTRAINT query_folders_slug_uq UNIQUE (space_id, slug);

ALTER TABLE spaces ADD COLUMN slug TEXT;
DO $baleia$
DECLARE
    r         RECORD;
    base      TEXT;
    candidate TEXT;
    n         INT;
BEGIN
    FOR r IN SELECT id, workspace_id, name, is_default FROM spaces ORDER BY created_at, id LOOP
        IF r.is_default THEN
            base := 'general';
        ELSE
            base := btrim(regexp_replace(lower(r.name), '[^a-z0-9]+', '_', 'g'), '_');
            IF base = '' THEN base := 'space'; END IF;
            IF base !~ '^[a-z]' THEN base := 'space_' || base; END IF;
            base := left(base, 63);
        END IF;
        candidate := base;
        n := 1;
        WHILE EXISTS (SELECT 1 FROM spaces WHERE workspace_id = r.workspace_id AND slug = candidate AND id <> r.id) LOOP
            n := n + 1;
            candidate := left(base, 63 - length('_' || n::text)) || '_' || n::text;
        END LOOP;
        UPDATE spaces SET slug = candidate WHERE id = r.id;
    END LOOP;
END $baleia$;
ALTER TABLE spaces
    ALTER COLUMN slug SET NOT NULL,
    ADD CONSTRAINT spaces_slug_format CHECK (slug ~ '^[a-z][a-z0-9_]{0,62}$'),
    ADD CONSTRAINT spaces_slug_uq UNIQUE (workspace_id, slug);

-- Semantic metrics are content items too (#33): they belong to a space.
ALTER TABLE semantic_metrics ADD COLUMN space_id UUID;
UPDATE semantic_metrics m SET space_id = s.id
  FROM spaces s WHERE s.workspace_id = m.workspace_id AND s.is_default;
ALTER TABLE semantic_metrics
    ALTER COLUMN space_id SET NOT NULL,
    ADD CONSTRAINT semantic_metrics_space_fk FOREIGN KEY (workspace_id, space_id)
        REFERENCES spaces (workspace_id, id) ON DELETE CASCADE;
CREATE INDEX semantic_metrics_space ON semantic_metrics (space_id);

-- Effective space role: the highest of (global admin / workspace owner) =
-- manager, the space's default_role for a workspace member when visibility is
-- 'workspace', and explicit user/team entries. A caller who is not a member of
-- the workspace has no role, even with a stale entry. NULL means "invisible".
CREATE FUNCTION baleia_effective_space_role(ws_id UUID, sp_id UUID, usr_id UUID)
RETURNS TEXT LANGUAGE sql STABLE AS $$
    WITH space AS (
        SELECT s.* FROM spaces s WHERE s.id = sp_id AND s.workspace_id = ws_id
    ),
    membership AS (
        SELECT
            -- A personal workspace is owned by its user; a shared one by the
            -- owners of its team (same rule as authz.IsWorkspaceOwner).
            (w.owner_user_id = usr_id) OR EXISTS (
                SELECT 1 FROM team_members tm
                WHERE tm.team_id = w.owner_team_id AND tm.user_id = usr_id AND tm.role = 'owner'
            ) AS is_owner,
            (w.owner_user_id = usr_id) OR EXISTS (
                SELECT 1 FROM team_members tm WHERE tm.team_id = w.owner_team_id AND tm.user_id = usr_id
            ) AS is_member
        FROM workspaces w WHERE w.id = ws_id
    ),
    candidates AS (
        SELECT 3 AS rank WHERE EXISTS (SELECT 1 FROM users u WHERE u.id = usr_id AND u.role = 'admin')
        UNION ALL
        SELECT 3 WHERE EXISTS (SELECT 1 FROM membership WHERE is_owner)
        UNION ALL
        SELECT CASE (SELECT default_role FROM space) WHEN 'editor' THEN 2 ELSE 1 END
        WHERE EXISTS (SELECT 1 FROM membership WHERE is_member)
          AND EXISTS (SELECT 1 FROM space WHERE visibility = 'workspace')
        UNION ALL
        SELECT CASE sm.role WHEN 'viewer' THEN 1 WHEN 'editor' THEN 2 WHEN 'manager' THEN 3 END
        FROM space_members sm
        WHERE EXISTS (SELECT 1 FROM membership WHERE is_member)
          AND sm.space_id = sp_id AND sm.principal_type = 'user' AND sm.principal_id = usr_id
        UNION ALL
        SELECT CASE sm.role WHEN 'viewer' THEN 1 WHEN 'editor' THEN 2 WHEN 'manager' THEN 3 END
        FROM space_members sm
        JOIN team_members tm ON tm.team_id = sm.principal_id AND tm.user_id = usr_id
        WHERE EXISTS (SELECT 1 FROM membership WHERE is_member)
          AND sm.space_id = sp_id AND sm.principal_type = 'team'
    )
    SELECT CASE max(rank) WHEN 3 THEN 'manager' WHEN 2 THEN 'editor' WHEN 1 THEN 'viewer' END
    FROM candidates
    HAVING max(rank) IS NOT NULL
$$;

-- Every new workspace gets its default space in the same transaction, whatever
-- the creation path (API, personal workspace on first login, future ones).
CREATE FUNCTION workspaces_create_default_space() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO spaces (workspace_id, name, visibility, default_role, is_default, slug)
    VALUES (NEW.id, 'General', 'workspace', 'editor', true, 'general');
    RETURN NEW;
END $$;
CREATE TRIGGER workspaces_default_space AFTER INSERT ON workspaces
    FOR EACH ROW EXECUTE FUNCTION workspaces_create_default_space();

-- Content versions (#34, ADR 0013): an append-only history of every saved query
-- and semantic metric, written in the same transaction as the change. A
-- version says what the content was; the audit trail says who did what, from
-- where. They are complementary, so versions are not derived from audit.

CREATE TABLE content_versions (
    id            BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    workspace_id  UUID NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    -- Space at the time of the version. No FK: a deleted space (and its
    -- content) must still be restorable from the version history.
    space_id      UUID NOT NULL,
    entity_type   TEXT NOT NULL CHECK (entity_type IN ('saved_query', 'semantic_metric')),
    -- No FK: versions outlive deleted entities so trash/restore can bring them
    -- back with their original id.
    entity_id     UUID NOT NULL,
    version       INTEGER NOT NULL CHECK (version >= 1),
    change_kind   TEXT NOT NULL CHECK (change_kind IN ('create', 'update', 'delete', 'restore', 'import')),
    -- Full state after the change (before, for a delete).
    snapshot      JSONB NOT NULL,
    author_id     UUID REFERENCES users (id) ON DELETE SET NULL,
    restored_from INTEGER,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (entity_type, entity_id, version)
);
CREATE INDEX content_versions_ws_time ON content_versions (workspace_id, created_at DESC);
CREATE INDEX content_versions_entity ON content_versions (entity_type, entity_id, version DESC);
CREATE INDEX content_versions_deleted ON content_versions (workspace_id, entity_type, created_at DESC)
    WHERE change_kind = 'delete';

-- Append-only, same mechanism as audit_events (#45). The one exception is the
-- cascade from a workspace deletion: the parent row is already gone when the
-- cascaded delete fires.
CREATE FUNCTION content_versions_append_only() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' AND NOT EXISTS (SELECT 1 FROM workspaces WHERE id = OLD.workspace_id) THEN
        RETURN OLD;
    END IF;
    RAISE EXCEPTION 'content_versions is append-only (% blocked)', TG_OP;
END $$;
CREATE TRIGGER content_versions_no_update BEFORE UPDATE OR DELETE ON content_versions
    FOR EACH ROW EXECUTE FUNCTION content_versions_append_only();

-- Baseline: version 1 for everything that exists today.
INSERT INTO content_versions (workspace_id, space_id, entity_type, entity_id, version, change_kind, snapshot, author_id, created_at)
SELECT workspace_id, space_id, 'saved_query', id, 1, 'create',
       jsonb_build_object(
           'schema', 'saved_query/v1',
           'slug', slug, 'name', name, 'sql', sql, 'folder_id', folder_id,
           'space_id', space_id, 'target_binding_id', target_binding_id, 'target_schema', target_schema
       ),
       created_by, updated_at
FROM saved_queries;

INSERT INTO content_versions (workspace_id, space_id, entity_type, entity_id, version, change_kind, snapshot, author_id, created_at)
SELECT workspace_id, space_id, 'semantic_metric', id, 1, 'create',
       to_jsonb(m) - 'id' - 'workspace_id' - 'updated_at' - 'updated_by'
           || jsonb_build_object('schema', 'semantic_metric/v1'),
       updated_by, updated_at
FROM semantic_metrics m;

-- Execution history (#36): a trigram index for substring search on SQL and a
-- composite index for the workspace's newest-first keyset pages.
CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE INDEX IF NOT EXISTS query_executions_sql_trgm
    ON query_executions USING gin (sql gin_trgm_ops);
CREATE INDEX IF NOT EXISTS query_executions_ws_started
    ON query_executions (workspace_id, started_at DESC, id DESC);

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

-- Correlation search (#49): the audit viewer filters by request_id and trace_id.
CREATE INDEX idx_audit_events_request_id ON audit_events (request_id) WHERE request_id IS NOT NULL;
CREATE INDEX idx_audit_events_trace_id ON audit_events (trace_id) WHERE trace_id IS NOT NULL;

-- Monitoring is per engine and table health per logical catalog (#117).
ALTER TABLE cluster_snapshots     ADD COLUMN engine_id UUID REFERENCES engines (id) ON DELETE CASCADE;
ALTER TABLE trino_query_snapshots ADD COLUMN engine_id UUID REFERENCES engines (id) ON DELETE CASCADE;

UPDATE cluster_snapshots
SET engine_id = (SELECT e.id FROM engines e JOIN trino_clusters t ON t.engine_id = e.id WHERE t.name = 'default');
UPDATE trino_query_snapshots
SET engine_id = (SELECT e.id FROM engines e JOIN trino_clusters t ON t.engine_id = e.id WHERE t.name = 'default');

ALTER TABLE cluster_snapshots     ALTER COLUMN engine_id SET NOT NULL;
ALTER TABLE trino_query_snapshots ALTER COLUMN engine_id SET NOT NULL;

CREATE INDEX cluster_snapshots_engine_time     ON cluster_snapshots (engine_id, captured_at DESC);
CREATE INDEX trino_query_snapshots_engine_time ON trino_query_snapshots (engine_id, created_at DESC);

ALTER TABLE table_health_snapshots ADD COLUMN catalog_id UUID REFERENCES catalogs (id) ON DELETE CASCADE;
UPDATE table_health_snapshots h
SET catalog_id = b.catalog_id
FROM catalog_bindings b
JOIN catalogs c ON c.id = b.catalog_id
WHERE c.connection_id = h.catalog_connection_id AND b.engine_catalog_name = h.catalog;
DELETE FROM table_health_snapshots WHERE catalog_id IS NULL;
ALTER TABLE table_health_snapshots ALTER COLUMN catalog_id SET NOT NULL;
ALTER TABLE table_health_snapshots
    DROP CONSTRAINT IF EXISTS table_health_snapshots_catalog_connection_id_catalog_namespace_table_name_key;
ALTER TABLE table_health_snapshots
    ADD CONSTRAINT table_health_unique_catalog UNIQUE (catalog_id, namespace, table_name);

CREATE TABLE maintenance_runs (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    catalog_id    UUID NOT NULL REFERENCES catalogs (id) ON DELETE CASCADE,
    binding_id    UUID REFERENCES catalog_bindings (id) ON DELETE SET NULL,
    namespace     TEXT NOT NULL,
    table_name    TEXT NOT NULL,
    operations    TEXT[] NOT NULL CHECK (operations <@ ARRAY['optimize','expire_snapshots','remove_orphan_files']),
    retention     TEXT,
    status        TEXT NOT NULL CHECK (status IN ('running','finished','failed')),
    error_code    TEXT,
    error_message TEXT,
    started_by    UUID REFERENCES users (id) ON DELETE SET NULL,
    started_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at   TIMESTAMPTZ,
    workflow_id   TEXT NOT NULL
);
CREATE INDEX maintenance_runs_catalog ON maintenance_runs (catalog_id, started_at DESC);

ALTER TABLE semantic_metrics
    ADD COLUMN certified_by UUID REFERENCES users (id) ON DELETE SET NULL,
    ADD COLUMN certified_at TIMESTAMPTZ;

UPDATE semantic_metrics SET certified_at = updated_at WHERE certified;

ALTER TABLE semantic_docs
    ADD COLUMN certified BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN certified_by UUID REFERENCES users (id) ON DELETE SET NULL,
    ADD COLUMN certified_at TIMESTAMPTZ;

-- Every refresh token belongs to a rotation family (#51). The first token of a
-- family uses its own id as family_id; rotated tokens inherit it so reuse
-- detection can revoke the whole family. Rows that predate the column are
-- backfilled to their own id, which is equivalent to one family per token.
ALTER TABLE refresh_tokens ADD COLUMN family_id UUID;
UPDATE refresh_tokens SET family_id = id WHERE family_id IS NULL;
ALTER TABLE refresh_tokens ALTER COLUMN family_id SET NOT NULL;

CREATE INDEX idx_refresh_tokens_family_id ON refresh_tokens(family_id);

-- Password-reset tokens are stored as SHA-256 hashes and are single use (#51).
-- A row is consumed by setting used_at; expired and used rows are pruned by the
-- daily token-cleanup schedule.
CREATE TABLE password_reset_tokens (
    token_hash TEXT PRIMARY KEY,
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at TIMESTAMPTZ NOT NULL,
    used_at    TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_password_reset_tokens_user_id ON password_reset_tokens(user_id);
CREATE INDEX idx_password_reset_tokens_expires_at ON password_reset_tokens(expires_at);

-- Outstanding links are invalidated on deploy: the plaintext columns are
-- removed rather than migrated.
ALTER TABLE users
    DROP COLUMN IF EXISTS password_reset_token,
    DROP COLUMN IF EXISTS password_reset_expires_at;

-- Server-side OIDC login attempts (#250). The state is the primary key and the
-- row is consumed atomically, so a replayed callback finds nothing. The PKCE
-- verifier never leaves the server; nonce is reserved for the nonce slice.
CREATE TABLE oidc_login_attempts (
    state         TEXT PRIMARY KEY,
    provider_id   UUID NOT NULL REFERENCES auth_providers(id) ON DELETE CASCADE,
    nonce         TEXT,
    code_verifier TEXT NOT NULL,
    redirect      TEXT,
    expires_at    TIMESTAMPTZ NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_oidc_login_attempts_expires_at ON oidc_login_attempts(expires_at);

-- Defence in depth for the stored post-login redirect (#252): the handler
-- validates it too, but the database refuses scheme-relative, backslash and
-- control-character escapes even if a future writer forgets.
ALTER TABLE oidc_login_attempts
    ADD CONSTRAINT oidc_login_attempts_redirect_check
    CHECK (
        redirect IS NULL
        OR (
            redirect LIKE '/%'
            AND redirect NOT LIKE '//%'
            AND redirect !~ '\\'
            AND redirect !~ '[\r\n\t]'
        )
    );

-- API-key metadata for the X-Api-Key middleware (#276). The secret is still
-- stored only as a hash; prefix is the displayable part of the raw key, scopes
-- are '*' today, and last_used_at is touched on successful authentication.
ALTER TABLE api_keys
    ADD COLUMN prefix       TEXT        NOT NULL DEFAULT '',
    ADD COLUMN scopes       TEXT[]      NOT NULL DEFAULT ARRAY['*'],
    ADD COLUMN last_used_at TIMESTAMPTZ;

-- Existing keys cannot recover their raw prefix; keep a stable hash prefix for
-- display until they are rotated.
UPDATE api_keys SET prefix = LEFT(key_hash, 8) WHERE prefix = '';

-- LDAP providers (#253). The type CHECK and the users.auth_provider CHECK are
-- inline constraints created by 00001, so PostgreSQL named them after the
-- column. chk_ldap_auth mirrors chk_oidc_auth: an LDAP user must be linked to
-- a provider and an external (directory) id.
ALTER TABLE auth_providers DROP CONSTRAINT auth_providers_type_check;
ALTER TABLE auth_providers ADD CONSTRAINT auth_providers_type_check
    CHECK (type IN ('oidc', 'ldap'));

ALTER TABLE users DROP CONSTRAINT users_auth_provider_check;
ALTER TABLE users ADD CONSTRAINT users_auth_provider_check
    CHECK (auth_provider IN ('local', 'oidc', 'ldap'));

ALTER TABLE users ADD CONSTRAINT chk_ldap_auth
    CHECK (auth_provider != 'ldap' OR (auth_provider_id IS NOT NULL AND external_id IS NOT NULL));

-- Suspend, never delete (#256). status is the only disable switch; the
-- suspended_* columns explain when and why.
ALTER TABLE users
    ADD COLUMN status              TEXT        NOT NULL DEFAULT 'active',
    ADD COLUMN suspended_at        TIMESTAMPTZ,
    ADD COLUMN suspended_reason    TEXT,
    ADD COLUMN directory_synced_at TIMESTAMPTZ;

ALTER TABLE users ADD CONSTRAINT users_status_check
    CHECK (status IN ('active', 'suspended'));

-- One row per directory reconciliation pass (#257). The run is append-only:
-- it records what the pass saw and changed, including a safety-brake abort.
CREATE TABLE directory_reconcile_runs (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    provider_id  UUID        NOT NULL REFERENCES auth_providers (id) ON DELETE CASCADE,
    started_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at  TIMESTAMPTZ,
    outcome      TEXT        CHECK (outcome IN ('success', 'error', 'aborted_safety')),
    seen         INTEGER     NOT NULL DEFAULT 0,
    suspended    INTEGER     NOT NULL DEFAULT 0,
    reinstated   INTEGER     NOT NULL DEFAULT 0,
    error        TEXT,
    actions      JSONB       NOT NULL DEFAULT '[]'::jsonb
);

CREATE INDEX idx_directory_reconcile_runs_provider
    ON directory_reconcile_runs (provider_id, started_at DESC);

-- Agents are non-human principals that act on behalf of a user (#59). They are
-- bound to workspaces, are always read-only, and authenticate only with a
-- `bal_agt_` token on /mcp. Assistant agents (kind='assistant') are managed by
-- #62 and never listed by the agents API.
CREATE TABLE agents (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_id    UUID        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    kind        TEXT        NOT NULL DEFAULT 'external' CHECK (kind IN ('external', 'assistant')),
    name        TEXT        NOT NULL,
    description TEXT        NOT NULL DEFAULT '',
    status      TEXT        NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'disabled')),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_agents_owner ON agents (owner_id);
CREATE INDEX idx_agents_kind  ON agents (kind);

-- The workspaces an agent may act in. Rights are the owner's member rights
-- intersected with this set; the binding is re-checked on every call.
CREATE TABLE agent_workspaces (
    agent_id     UUID NOT NULL REFERENCES agents (id) ON DELETE CASCADE,
    workspace_id UUID NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    PRIMARY KEY (agent_id, workspace_id)
);

CREATE INDEX idx_agent_workspaces_workspace ON agent_workspaces (workspace_id);

CREATE TABLE agent_tokens (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    agent_id     UUID        NOT NULL REFERENCES agents (id) ON DELETE CASCADE,
    name         TEXT        NOT NULL,
    token_hash   TEXT        NOT NULL UNIQUE,
    prefix       TEXT        NOT NULL,
    expires_at   TIMESTAMPTZ NOT NULL,
    last_used_at TIMESTAMPTZ,
    created_by   UUID        NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at   TIMESTAMPTZ
);

CREATE INDEX idx_agent_tokens_agent ON agent_tokens (agent_id);

-- Agent-caused executions are attributable and quota-counted (#59, #60).
ALTER TABLE query_executions
    ADD COLUMN agent_id UUID REFERENCES agents (id) ON DELETE SET NULL;

CREATE INDEX idx_query_executions_agent ON query_executions (agent_id);

-- Per-agent capability policy (#60). `{}` means "defaults"; the effective
-- policy is defaults ⊕ stored fields, clamped by the server ceilings.
ALTER TABLE agents
    ADD COLUMN policy            JSONB       NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN policy_updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    ADD COLUMN policy_updated_by UUID        REFERENCES users (id) ON DELETE SET NULL;

-- Concurrency and hourly quotas are computed from durable executions (#60).
CREATE INDEX idx_query_executions_agent_started
    ON query_executions (agent_id, started_at DESC);

-- The owner an agent acts for (#61). NULL for human and service actors; set
-- for every event caused by an agent. ALTER TABLE on the partitioned parent
-- propagates to the existing partitions.
ALTER TABLE audit_events ADD COLUMN on_behalf_of UUID;

CREATE INDEX audit_events_on_behalf_of
    ON audit_events (on_behalf_of, occurred_at DESC)
    WHERE on_behalf_of IS NOT NULL;

-- Assistant configuration (#62). One row; the API key is sealed with the
-- Keyring under AAD "assistant:api_key" (#19) and never returned.
CREATE TABLE assistant_settings (
    id                        BOOLEAN PRIMARY KEY DEFAULT true CHECK (id),
    enabled                   BOOLEAN NOT NULL DEFAULT false,
    provider                  TEXT CHECK (provider IN ('anthropic', 'openai_compatible')),
    base_url                  TEXT,
    model                     TEXT,
    api_key_enc               TEXT,
    data_sharing              TEXT NOT NULL DEFAULT 'metadata_only'
                              CHECK (data_sharing IN ('metadata_only', 'metadata_and_samples')),
    max_turn_seconds          INTEGER NOT NULL DEFAULT 180,
    max_tool_calls_per_turn   INTEGER NOT NULL DEFAULT 10,
    max_output_tokens_per_turn INTEGER NOT NULL DEFAULT 4096,
    daily_token_budget_per_user BIGINT NOT NULL DEFAULT 500000,
    updated_by                UUID REFERENCES users (id) ON DELETE SET NULL,
    updated_at                TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO assistant_settings DEFAULT VALUES;

-- A conversation belongs to one user and one workspace; the assistant agent is
-- provisioned per user × workspace and owns the tool calls.
CREATE TABLE assistant_conversations (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id           UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    workspace_id      UUID NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    agent_id          UUID NOT NULL REFERENCES agents (id) ON DELETE CASCADE,
    title             TEXT NOT NULL DEFAULT '',
    state             TEXT NOT NULL DEFAULT 'idle'
                      CHECK (state IN ('idle', 'running', 'interrupted')),
    turn_owner        TEXT,
    turn_heartbeat_at TIMESTAMPTZ,
    last_seq          INTEGER NOT NULL DEFAULT 0,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX assistant_conversations_user ON assistant_conversations (user_id, updated_at DESC);

-- History is append-only and keyed by a per-conversation sequence so SSE
-- clients can replay with after_seq. content is typed per role by the service.
CREATE TABLE assistant_messages (
    conversation_id UUID NOT NULL REFERENCES assistant_conversations (id) ON DELETE CASCADE,
    seq             INTEGER NOT NULL,
    role            TEXT NOT NULL
                    CHECK (role IN ('user', 'assistant', 'tool_call', 'tool_result', 'notice')),
    content         JSONB NOT NULL,
    input_tokens    INTEGER,
    output_tokens   INTEGER,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (conversation_id, seq)
);

CREATE INDEX assistant_messages_usage ON assistant_messages (created_at)
    WHERE output_tokens IS NOT NULL;

-- UI language preference (#67). NULL means "follow the browser"; the locale
-- selection lives with the user so it follows them across devices.
ALTER TABLE users
    ADD COLUMN locale TEXT;

ALTER TABLE users ADD CONSTRAINT users_locale_check
    CHECK (locale IN ('en', 'pt-BR'));

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

-- Per-table maintenance parameters (#500). A table without a row is not
-- maintained. Each operation (Trino's optimize, optimize_manifests,
-- expire_snapshots, remove_orphan_files) has its own JSON settings: trigger,
-- min/max interval, window, timeout, conflict policy and params.
CREATE TABLE table_maintenance_policies (
    id                             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    catalog_id                     UUID NOT NULL REFERENCES catalogs (id) ON DELETE CASCADE,
    namespace                      TEXT NOT NULL,
    table_name                     TEXT NOT NULL,
    mode                           TEXT NOT NULL CHECK (mode IN ('managed', 'off', 'maintained_elsewhere')),
    -- The writer's commit or checkpoint interval: the hot-file and orphan
    -- windows must be longer than this.
    writer_commit_interval_seconds INTEGER NOT NULL DEFAULT 60 CHECK (writer_commit_interval_seconds > 0),
    optimize                       JSONB NOT NULL DEFAULT '{}'::jsonb,
    optimize_manifests             JSONB NOT NULL DEFAULT '{}'::jsonb,
    expire_snapshots               JSONB NOT NULL DEFAULT '{}'::jsonb,
    remove_orphan_files            JSONB NOT NULL DEFAULT '{}'::jsonb,
    revision                       BIGINT NOT NULL DEFAULT 1,
    created_by                     UUID REFERENCES users (id) ON DELETE SET NULL,
    updated_by                     UUID REFERENCES users (id) ON DELETE SET NULL,
    created_at                     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at                     TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (catalog_id, namespace, table_name)
);
CREATE INDEX table_maintenance_policies_managed
    ON table_maintenance_policies (catalog_id) WHERE mode = 'managed';

-- Runs record what triggered them, why they were due, the settings they
-- used (frozen) and how many attempts a commit conflict took.
ALTER TABLE maintenance_runs
    ADD COLUMN trigger         TEXT NOT NULL DEFAULT 'manual' CHECK (trigger IN ('manual', 'automatic', 'dry_run')),
    ADD COLUMN reason          JSONB NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN parameters      JSONB NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN attempts        INTEGER NOT NULL DEFAULT 1 CHECK (attempts > 0),
    ADD COLUMN policy_revision BIGINT;

ALTER TABLE maintenance_runs DROP CONSTRAINT maintenance_runs_status_check;
ALTER TABLE maintenance_runs ADD CONSTRAINT maintenance_runs_status_check
    CHECK (status IN ('running', 'finished', 'failed', 'gave_up', 'dry_run'));

ALTER TABLE maintenance_runs DROP CONSTRAINT maintenance_runs_operations_check;
ALTER TABLE maintenance_runs ADD CONSTRAINT maintenance_runs_operations_check
    CHECK (operations <@ ARRAY['optimize', 'optimize_manifests', 'expire_snapshots', 'remove_orphan_files']);

-- At most one running run per table, so manual and automatic runs never
-- overlap. Runs left 'running' by a crashed worker would break the index:
-- keep the newest per table and close the others.
UPDATE maintenance_runs r
SET status = 'failed', error_code = 'superseded', finished_at = COALESCE(r.finished_at, now())
WHERE r.status = 'running'
  AND EXISTS (
      SELECT 1 FROM maintenance_runs n
      WHERE n.status = 'running'
        AND n.catalog_id = r.catalog_id AND n.namespace = r.namespace AND n.table_name = r.table_name
        AND (n.started_at, n.id) > (r.started_at, r.id)
  );
CREATE UNIQUE INDEX maintenance_runs_one_running
    ON maintenance_runs (catalog_id, namespace, table_name) WHERE status = 'running';
CREATE INDEX maintenance_runs_table
    ON maintenance_runs (catalog_id, namespace, table_name, started_at DESC);

-- Health metrics that tell a CDC or streaming table from a batch one (#500),
-- the operations a table's parameters find due, and the Iceberg properties
-- the form starts from. needs_orphan_cleanup was never computed: orphan
-- cleanup is driven by its maximum interval instead.
ALTER TABLE table_health_snapshots
    ADD COLUMN delete_file_count     BIGINT,
    ADD COLUMN equality_delete_count BIGINT,
    ADD COLUMN position_delete_count BIGINT,
    ADD COLUMN small_file_count      BIGINT,
    ADD COLUMN commits_last_hour     INTEGER,
    ADD COLUMN commits_last_day      INTEGER,
    ADD COLUMN avg_file_size_bytes   BIGINT,
    ADD COLUMN manifest_count        INTEGER,
    ADD COLUMN format_version        INTEGER,
    ADD COLUMN table_properties      JSONB NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN due_operations        TEXT[] NOT NULL DEFAULT '{}',
    ADD COLUMN policy_revision       BIGINT;
ALTER TABLE table_health_snapshots DROP COLUMN needs_orphan_cleanup;

-- Workload management (#399, ADR 0014): a run can wait in a per-engine queue
-- before it reaches the engine. Capacity is configured per (engine, class);
-- a missing row means the default that the API resolves.

ALTER TABLE query_executions DROP CONSTRAINT IF EXISTS query_executions_status_check;
ALTER TABLE query_executions
    ADD CONSTRAINT query_executions_status_check
    CHECK (status IN ('running', 'finished', 'failed', 'cancelled', 'queued'));

ALTER TABLE query_executions
    ADD COLUMN queue_class   TEXT CHECK (queue_class IS NULL OR queue_class IN ('interactive', 'heavy', 'background')),
    ADD COLUMN queue_reason  TEXT,
    ADD COLUMN fairness_key  TEXT,
    ADD COLUMN queued_at     TIMESTAMPTZ,
    ADD COLUMN admitted_at   TIMESTAMPTZ,
    ADD COLUMN queue_wait_ms BIGINT;

-- Existing runs never waited in a queue.
UPDATE query_executions
   SET queued_at = started_at, admitted_at = started_at
 WHERE queued_at IS NULL;

CREATE TABLE engine_queue_capacity (
    engine_id        UUID        NOT NULL REFERENCES engines (id) ON DELETE CASCADE,
    class            TEXT        NOT NULL CHECK (class IN ('interactive', 'heavy', 'background')),
    max_running      INTEGER     NOT NULL CHECK (max_running >= 0),
    max_queued       INTEGER     NOT NULL CHECK (max_queued >= 0),
    queue_timeout_ms BIGINT      NOT NULL CHECK (queue_timeout_ms > 0),
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (engine_id, class)
);

-- Nullable: no ceiling unless an admin sets one.
ALTER TABLE engines
    ADD COLUMN max_running_total INTEGER CHECK (max_running_total IS NULL OR max_running_total >= 0);

-- A run can fail before any statement runs — e.g. the admission queue timed
-- out or was full (#399). It carries an error code with no statement to hold
-- it, so the execution itself records it.
ALTER TABLE query_executions ADD COLUMN error_code TEXT;

-- Runtime bound per workload class (#576): a run that waited up to 24h in the
-- queue must not be killed by the 30m global statement timeout. NULL keeps the
-- class default resolved by the API.
ALTER TABLE engine_queue_capacity
    ADD COLUMN runtime_timeout_ms BIGINT
    CHECK (runtime_timeout_ms IS NULL OR runtime_timeout_ms > 0);

-- Ordered, admin-managed rules that set a run's class or max class (#399,
-- ADR 0014). `match` is a closed schema: the CHECK rejects any key outside the
-- allow-list, and the class columns reject an unknown class.
CREATE TABLE workload_rules (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    position   INTEGER NOT NULL,
    enabled    BOOLEAN NOT NULL DEFAULT true,
    match      JSONB NOT NULL DEFAULT '{}'::jsonb
        CHECK (match - ARRAY[
            'workspace_id', 'team_id', 'role', 'user_id', 'agent_id', 'engine_id',
            'origin', 'saved_query_id', 'min_statements', 'max_statements'
        ] = '{}'::jsonb),
    class      TEXT NOT NULL CHECK (class IN ('interactive', 'heavy', 'background')),
    max_class  TEXT CHECK (max_class IS NULL OR max_class IN ('interactive', 'heavy', 'background')),
    weight     INTEGER NOT NULL DEFAULT 1 CHECK (weight > 0),
    created_by UUID REFERENCES users (id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX workload_rules_position_idx ON workload_rules (position);

-- Optional cost routing for Trino (#550). When enabled, classification runs
-- EXPLAIN (FORMAT JSON) for a SELECT and moves a run whose estimate exceeds
-- the threshold into the heavy class.
ALTER TABLE engines
    ADD COLUMN cost_routing_enabled BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN cost_threshold_bytes BIGINT
        CHECK (cost_threshold_bytes IS NULL OR cost_threshold_bytes > 0);

-- Tables a statement reads and the equi-join predicates it uses (B0, #530,
-- #538). Written after every finished statement and by the one-off backfill.
CREATE TABLE statement_references (
    statement_id UUID NOT NULL REFERENCES query_statements (id) ON DELETE CASCADE,
    workspace_id UUID NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    -- The target's catalog. NULL when the target had none.
    catalog_id   UUID REFERENCES catalogs (id) ON DELETE SET NULL,
    namespace    TEXT NOT NULL DEFAULT '',
    table_name   TEXT NOT NULL,
    alias        TEXT NOT NULL DEFAULT '',
    kind         TEXT NOT NULL DEFAULT 'read' CHECK (kind IN ('read', 'write')),
    PRIMARY KEY (statement_id, namespace, table_name, alias)
);

CREATE INDEX statement_references_workspace_table
    ON statement_references (workspace_id, namespace, table_name);
CREATE INDEX statement_references_statement ON statement_references (statement_id);

CREATE TABLE statement_joins (
    statement_id UUID NOT NULL REFERENCES query_statements (id) ON DELETE CASCADE,
    workspace_id UUID NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    left_alias   TEXT NOT NULL,
    left_column  TEXT NOT NULL,
    right_alias  TEXT NOT NULL,
    right_column TEXT NOT NULL,
    PRIMARY KEY (statement_id, left_alias, left_column, right_alias, right_column)
);

CREATE INDEX statement_joins_workspace ON statement_joins (workspace_id);

-- The address BI clients reach, which may differ from the in-cluster URL
-- (#533, #560). Nullable: an engine without it cannot be opened from a BI tool.
ALTER TABLE engines ADD COLUMN external_url TEXT;

-- Foreign keys of the physical metadata snapshot (B1, #554): one column of a
-- table referencing one column of another. Read from the database providers
-- (PostgreSQL, MySQL, SQL Server, Oracle) in the same pass as the columns.
-- The referenced table is stored by name because a table's keys are written
-- before the whole catalog is walked; a read resolves it against
-- metadata_tables.
CREATE TABLE metadata_foreign_keys (
    catalog_id    UUID        NOT NULL REFERENCES catalogs (id) ON DELETE CASCADE,
    table_id      UUID        NOT NULL REFERENCES metadata_tables (id) ON DELETE CASCADE,
    column_name   TEXT        NOT NULL,
    ref_namespace TEXT[]      NOT NULL,
    ref_table     TEXT        NOT NULL,
    ref_column    TEXT        NOT NULL,
    last_seen_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (table_id, column_name, ref_namespace, ref_table, ref_column)
);

CREATE INDEX metadata_foreign_keys_catalog ON metadata_foreign_keys (catalog_id);
CREATE INDEX metadata_foreign_keys_ref ON metadata_foreign_keys (catalog_id, ref_namespace, ref_table);

-- Popular tables per workspace (B7, #562): a scheduled pass counts the reads
-- of the last 30 days from statement_references with a 7-day half-life decay.
-- The counts rank search and completions and feed the explorer's "popular"
-- list; they are materialized so a read never scans the history.
CREATE TABLE table_popularity (
    workspace_id UUID             NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    catalog_id   UUID             NOT NULL REFERENCES catalogs (id) ON DELETE CASCADE,
    -- The one namespace level statement_references stores (the schema).
    namespace    TEXT             NOT NULL DEFAULT '',
    table_name   TEXT             NOT NULL,
    uses         BIGINT           NOT NULL DEFAULT 0,
    score        DOUBLE PRECISION NOT NULL DEFAULT 0,
    last_used_at TIMESTAMPTZ      NOT NULL,
    computed_at  TIMESTAMPTZ      NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, catalog_id, namespace, table_name)
);

CREATE INDEX table_popularity_workspace ON table_popularity (workspace_id, score DESC);

-- Trino's database resource group manager (#599, A-5.2): Baleia writes the
-- generated resource-groups.json (#598) here, idempotently, one revision at a
-- time, and Trino reloads it from the same Postgres every second without a
-- restart (resource-groups.configuration-manager=db).
--
-- The first three tables mirror what Trino 484's db manager reads; the last
-- one is Baleia's own revision log, used for rollback. Trino matches the
-- `environment` column against node.environment, so Baleia scopes every write
-- to the cluster's environment.

CREATE TABLE resource_groups (
    resource_group_id            BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name                         VARCHAR(250) NOT NULL,
    soft_memory_limit            VARCHAR(128) NOT NULL,
    max_queued                   INTEGER      NOT NULL,
    soft_concurrency_limit       INTEGER,
    hard_concurrency_limit       INTEGER      NOT NULL,
    scheduling_policy            VARCHAR(128),
    scheduling_weight            INTEGER,
    jmx_export                   BOOLEAN,
    soft_cpu_limit               VARCHAR(128),
    hard_cpu_limit               VARCHAR(128),
    hard_physical_data_scan_limit VARCHAR(128),
    parent                       BIGINT REFERENCES resource_groups (resource_group_id) ON DELETE CASCADE,
    environment                  VARCHAR(128)
);

CREATE TABLE selectors (
    id                      BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    resource_group_id       BIGINT       NOT NULL REFERENCES resource_groups (resource_group_id) ON DELETE CASCADE,
    priority                BIGINT       NOT NULL,
    user_regex              VARCHAR(512),
    original_user_regex     VARCHAR(512),
    authenticated_user_regex VARCHAR(512),
    user_group_regex        VARCHAR(512),
    source_regex            VARCHAR(512),
    query_type              VARCHAR(128),
    -- A JSON array of tags, e.g. '["baleia.class=interactive"]'.
    client_tags             TEXT
);

CREATE TABLE resource_groups_global_properties (
    name  VARCHAR(128) PRIMARY KEY,
    value VARCHAR(512)
);

-- Baleia's revision log: every write is a new revision, so a rollback re-applies
-- the config of an earlier one. `config` is the full generated document.
CREATE TABLE trino_resource_group_revisions (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    engine_id  UUID        NOT NULL REFERENCES engines (id) ON DELETE CASCADE,
    revision   INTEGER     NOT NULL,
    config     JSONB       NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (engine_id, revision)
);

-- Trino's db resource group manager reads the tables above through the same
-- baleia_trino role the catalog-store plugin uses (migration 00018).
GRANT SELECT ON resource_groups TO baleia_trino;
GRANT SELECT ON selectors TO baleia_trino;
GRANT SELECT ON resource_groups_global_properties TO baleia_trino;

-- A-5.3 (#600): the per-engine switch that hands admission over to Trino's
-- resource groups. When on, Baleia's admission workflow stops granting slots
-- from its own per-class queue for this engine and lets Trino's resource group
-- hold the run; Baleia keeps only the parts Trino cannot express (fairness and
-- the per-agent limit). A run never waits in both queues.

ALTER TABLE engines
    ADD COLUMN trino_resource_groups_enabled BOOLEAN NOT NULL DEFAULT false;

-- #651: Baleia no longer imposes read-only work on a workspace's catalogs. A
-- catalog linked to a workspace stays reachable by that workspace's members
-- only; what they may read or write in it is decided by the engine and the
-- catalog manager, or by the connection's service account. The grant's
-- read/write permission and the never-enforced default_readonly flag are no
-- longer read or written by the application. credentials_read_only stays: it
-- is the administrator's own statement that the connection's account is
-- read-only at the source.
--
-- The two columns stay for this release: API pods of the previous release
-- still name them during a rolling upgrade, so dropping them now would fail
-- their queries. New code inserts grants without a permission, so the column
-- gets a default. Dropping both columns is #654, in the next release.

ALTER TABLE workspace_catalog_grants ALTER COLUMN permission SET DEFAULT 'write';

-- #648: the identity-provider tokens of a user's latest OIDC sign-in. Baleia
-- sends the access token through Trino to catalog managers set to per-user
-- identity, which authorize that user themselves, and refreshes it with the
-- refresh token while it is valid. Both are sealed with the keyring (bound to
-- the user id) and never leave the server otherwise. A sign-out, a disabled
-- account or a removed provider deletes the row.
CREATE TABLE user_idp_tokens (
    user_id           UUID        PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    auth_provider_id  UUID        NOT NULL REFERENCES auth_providers (id) ON DELETE CASCADE,
    access_token_enc  TEXT        NOT NULL,
    refresh_token_enc TEXT,
    expires_at        TIMESTAMPTZ NOT NULL,
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- #653: what a user may do in a workspace is their workspace role (admin,
-- editor, viewer); what they may read or write in a catalog stays with the
-- catalog manager (ADR 0015). Members of a shared workspace are its team's
-- members; a personal workspace is its owner's and whoever they share it with.
-- A row here sets a member's role; without one, a shared workspace keeps
-- today's abilities: team owner = admin, team member = editor.
CREATE TABLE workspace_members (
    workspace_id UUID        NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    user_id      UUID        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    role         TEXT        NOT NULL CHECK (role IN ('admin', 'editor', 'viewer')),
    granted_by   UUID        REFERENCES users (id) ON DELETE SET NULL,
    granted_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, user_id)
);
CREATE INDEX workspace_members_user ON workspace_members (user_id);

-- The caller's role in the workspace, NULL for a non-member. A global admin is
-- not a member by this function; authz adds that.
CREATE FUNCTION baleia_workspace_member_role(ws_id UUID, usr_id UUID)
RETURNS TEXT LANGUAGE sql STABLE AS $$
    SELECT CASE
        WHEN w.type = 'personal' AND w.owner_user_id = usr_id THEN 'admin'
        WHEN w.type = 'personal' THEN wm.role
        WHEN tm.user_id IS NULL THEN NULL
        ELSE COALESCE(wm.role, CASE tm.role WHEN 'owner' THEN 'admin' ELSE 'editor' END)
    END
    FROM workspaces w
    LEFT JOIN team_members tm ON tm.team_id = w.owner_team_id AND tm.user_id = usr_id
    LEFT JOIN workspace_members wm ON wm.workspace_id = w.id AND wm.user_id = usr_id
    WHERE w.id = ws_id
$$;

-- Space roles only narrow (#653): the effective role is the highest of the
-- space's grants (a workspace admin is a manager everywhere, a member gets the
-- space's default role when it is workspace-visible, explicit user/team
-- entries), capped by the workspace role: viewer -> viewer, editor -> editor,
-- admin -> manager. A global admin is a manager. A non-member has no role,
-- even with a stale entry. NULL means "invisible".
CREATE OR REPLACE FUNCTION baleia_effective_space_role(ws_id UUID, sp_id UUID, usr_id UUID)
RETURNS TEXT LANGUAGE sql STABLE AS $$
    WITH space AS (
        SELECT s.* FROM spaces s WHERE s.id = sp_id AND s.workspace_id = ws_id
    ),
    ws_role AS (
        SELECT CASE
            WHEN EXISTS (SELECT 1 FROM users u WHERE u.id = usr_id AND u.role = 'admin') THEN 'admin'
            ELSE baleia_workspace_member_role(ws_id, usr_id)
        END AS role
    ),
    cap AS (
        SELECT CASE role WHEN 'admin' THEN 3 WHEN 'editor' THEN 2 WHEN 'viewer' THEN 1 END AS rank
        FROM ws_role WHERE role IS NOT NULL
    ),
    candidates AS (
        SELECT 3 AS rank WHERE EXISTS (SELECT 1 FROM ws_role WHERE role = 'admin')
        UNION ALL
        SELECT CASE (SELECT default_role FROM space) WHEN 'editor' THEN 2 ELSE 1 END
        WHERE EXISTS (SELECT 1 FROM space WHERE visibility = 'workspace')
        UNION ALL
        SELECT CASE sm.role WHEN 'viewer' THEN 1 WHEN 'editor' THEN 2 WHEN 'manager' THEN 3 END
        FROM space_members sm
        WHERE sm.space_id = sp_id AND sm.principal_type = 'user' AND sm.principal_id = usr_id
        UNION ALL
        SELECT CASE sm.role WHEN 'viewer' THEN 1 WHEN 'editor' THEN 2 WHEN 'manager' THEN 3 END
        FROM space_members sm
        JOIN team_members tm ON tm.team_id = sm.principal_id AND tm.user_id = usr_id
        WHERE sm.space_id = sp_id AND sm.principal_type = 'team'
    )
    SELECT CASE least(max(c.rank), (SELECT rank FROM cap))
        WHEN 3 THEN 'manager' WHEN 2 THEN 'editor' WHEN 1 THEN 'viewer' END
    FROM candidates c
    WHERE EXISTS (SELECT 1 FROM cap) AND EXISTS (SELECT 1 FROM space)
    HAVING max(c.rank) IS NOT NULL
$$;

-- #653: Baleia service accounts, the non-human principals agents and other
-- clients sign in as. A service account is a user of kind 'service_account':
-- it has workspace roles like a person, never the global admin role, and no
-- password. It signs in with a Baleia token (an API key) or, when linked to an
-- identity provider (auth_provider 'oidc', external_id 'client:<client id>'),
-- with that provider's client-credentials access token, which is then also
-- its identity towards per-user catalogs (ADR 0015). home_workspace_id is the
-- workspace it was created in, whose admins manage it.
ALTER TABLE users
    ADD COLUMN kind TEXT NOT NULL DEFAULT 'human' CHECK (kind IN ('human', 'service_account')),
    ADD COLUMN home_workspace_id UUID REFERENCES workspaces (id) ON DELETE SET NULL,
    ADD COLUMN description TEXT;

ALTER TABLE users DROP CONSTRAINT users_auth_provider_check;
ALTER TABLE users ADD CONSTRAINT users_auth_provider_check
    CHECK (auth_provider IN ('local', 'oidc', 'ldap', 'service'));
ALTER TABLE users ADD CONSTRAINT chk_service_auth
    CHECK (auth_provider <> 'service' OR kind = 'service_account');
ALTER TABLE users ADD CONSTRAINT chk_service_account
    CHECK (kind <> 'service_account' OR (role = 'member' AND password_hash IS NULL AND auth_provider IN ('service', 'oidc')));

-- A service account belongs to a shared workspace through its row here, not
-- through the team; everything else is as in 00075.
CREATE OR REPLACE FUNCTION baleia_workspace_member_role(ws_id UUID, usr_id UUID)
RETURNS TEXT LANGUAGE sql STABLE AS $$
    SELECT CASE
        WHEN w.type = 'personal' AND w.owner_user_id = usr_id THEN 'admin'
        WHEN w.type = 'personal' THEN wm.role
        WHEN u.kind = 'service_account' THEN wm.role
        WHEN tm.user_id IS NULL THEN NULL
        ELSE COALESCE(wm.role, CASE tm.role WHEN 'owner' THEN 'admin' ELSE 'editor' END)
    END
    FROM workspaces w
    LEFT JOIN users u ON u.id = usr_id
    LEFT JOIN team_members tm ON tm.team_id = w.owner_team_id AND tm.user_id = usr_id
    LEFT JOIN workspace_members wm ON wm.workspace_id = w.id AND wm.user_id = usr_id
    WHERE w.id = ws_id
$$;

-- #653: a workspace role comes from a workspace admin ('manual') or from the
-- member's identity-provider groups ('idp_group'), refreshed at each sign-in.
-- A manual role is never overwritten by a group; a group role is removed when
-- the member leaves the group.
ALTER TABLE workspace_members
    ADD COLUMN source TEXT NOT NULL DEFAULT 'manual' CHECK (source IN ('manual', 'idp_group'));

-- Applies a sign-in's group roles (#653). Volatile PL/pgSQL so that, after
-- the per-user lock is taken, each statement reads what earlier sign-ins
-- committed (a single SQL statement keeps the snapshot it started with).
-- Only workspaces of teams the member is in; never a personal workspace;
-- never a role an admin set.
CREATE FUNCTION baleia_apply_group_workspace_roles(usr_id UUID, assignments JSONB)
RETURNS VOID LANGUAGE plpgsql VOLATILE AS $$
DECLARE
    keep UUID[];
BEGIN
    PERFORM pg_advisory_xact_lock(653, hashtext(usr_id::text));

    SELECT COALESCE(array_agg(w.id), '{}') INTO keep
    FROM jsonb_array_elements(assignments) AS e
    JOIN workspaces w ON w.id = (e ->> 'workspace_id')::uuid AND w.type = 'shared'
    JOIN team_members tm ON tm.team_id = w.owner_team_id AND tm.user_id = usr_id;

    INSERT INTO workspace_members (workspace_id, user_id, role, source)
    SELECT (e ->> 'workspace_id')::uuid, usr_id, e ->> 'role', 'idp_group'
    FROM jsonb_array_elements(assignments) AS e
    WHERE (e ->> 'workspace_id')::uuid = ANY (keep)
    ON CONFLICT (workspace_id, user_id) DO UPDATE
    SET role = EXCLUDED.role, granted_at = now()
    WHERE workspace_members.source = 'idp_group';

    DELETE FROM workspace_members d
    WHERE d.user_id = usr_id AND d.source = 'idp_group'
      AND NOT (d.workspace_id = ANY (keep));
END
$$;

-- #694: an immutable owner marker for the platform's own connections (the
-- default Trino cluster, the operator's catalog managers). created_by IS NULL
-- alone cannot tell a system-owned connection from one whose owner was deleted
-- (00015 uses ON DELETE SET NULL), and only the former may reach a private
-- address through the managed dialer (#609).
ALTER TABLE catalog_connections ADD COLUMN system_owned BOOLEAN NOT NULL DEFAULT false;

-- Existing rows keep today's behavior: every created_by IS NULL row was
-- treated as system-owned before the marker. New orphans (created_by nulled by
-- a user deletion) keep false and stay on the strict dialer.
UPDATE catalog_connections SET system_owned = true WHERE created_by IS NULL;

-- #702 / ADR 0018: the workspace catalog scope. One SQL function becomes the
-- single source of truth for "which catalogs a workspace may use"; a
-- pending-request table carries the two-party grant flow; and the #654 columns
-- go away with the personal auto-reach rule.

-- The scope: the only reader of workspace_catalog_grants outside grant CRUD.
CREATE FUNCTION baleia_workspace_catalog_ids(ws uuid) RETURNS SETOF uuid
LANGUAGE sql STABLE AS $$
    SELECT g.catalog_id
    FROM workspace_catalog_grants g
    JOIN catalogs c ON c.id = g.catalog_id
    WHERE g.workspace_id = ws AND c.enabled
$$;

-- Pending grant requests, kept apart from the active grants table.
CREATE TABLE catalog_grant_requests (
    workspace_id      UUID        NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    catalog_id        UUID        NOT NULL REFERENCES catalogs(id)   ON DELETE CASCADE,
    initiated_by_side TEXT        NOT NULL CHECK (initiated_by_side IN ('catalog', 'workspace')),
    requested_by      UUID        REFERENCES users(id) ON DELETE SET NULL,
    requested_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, catalog_id)
);

-- Discoverability for the workspace-admin request picker.
ALTER TABLE catalogs ADD COLUMN discoverable BOOLEAN NOT NULL DEFAULT false;

-- #654 (`permission`, `default_readonly`) stays for now: a 0.6.1 pod still
-- names both during the rolling update (`RETURNING *` on a grant read,
-- `SELECT *` on every connection read), so dropping them here would break
-- those pods. They are dropped in a later release, once 0.6.1 is gone.

-- Backfill: an explicit grant for every (personal workspace, catalog of the
-- owner's connections) pair the personal auto-reach rule reached, so nobody
-- loses access when the rule is dropped. Not reversed in Down: a backfilled
-- grant is indistinguishable from a real one.
INSERT INTO workspace_catalog_grants (workspace_id, catalog_id, granted_by)
SELECT w.id, c.id, w.owner_user_id
FROM workspaces w
JOIN catalog_connections cc ON cc.created_by = w.owner_user_id
JOIN catalogs c ON c.connection_id = cc.id
WHERE w.type = 'personal' AND w.owner_user_id IS NOT NULL
ON CONFLICT (workspace_id, catalog_id) DO NOTHING;

-- #654: the grant's read/write permission and the never-enforced
-- default_readonly flag are no longer read or written (#651). They stayed
-- through 0.6.1 and 0.6.2 so a pod from the previous release could finish its
-- rolling update while still naming them; now they are dropped.
ALTER TABLE workspace_catalog_grants DROP COLUMN permission;
ALTER TABLE catalog_connections DROP COLUMN default_readonly;

-- #782: the system_owned marker existed only to route system-owned connections
-- to the managed dialer (RFC1918 open). The dialer is now fully config-driven
-- (BALEIA_CONNECTIONS_DENY_CIDRS / _ALLOW_CIDRS), so the split is redundant:
-- every connection uses the same dialer and the deployer decides the policy.
ALTER TABLE catalog_connections DROP COLUMN system_owned;
