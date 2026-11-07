package migrations

import (
	"context"

	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(up00071, down00071)
}

const up00071Postgres = `
-- Trino's database resource group manager (#599, A-5.2): Baleia writes the
-- generated resource-groups.json (#598) here, idempotently, one revision at a
-- time, and Trino reloads it from the same Postgres every second without a
-- restart (resource-groups.configuration-manager=db).
--
-- The first three tables mirror what Trino 484's db manager reads; the last
-- one is Baleia's own revision log, used for rollback. Trino matches the
-- 'environment' column against node.environment, so Baleia scopes every write
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
-- the config of an earlier one. 'config' is the full generated document.
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
`

const up00071MySQL = `CREATE TABLE resource_groups (resource_group_id            BIGINT AUTO_INCREMENT PRIMARY KEY,
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
    parent                       BIGINT,
    environment                  VARCHAR(128),
    CONSTRAINT resource_groups_parent_fkey FOREIGN KEY (parent) REFERENCES resource_groups (resource_group_id) ON DELETE CASCADE) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
CREATE TABLE selectors (id                      BIGINT AUTO_INCREMENT PRIMARY KEY,
    resource_group_id       BIGINT       NOT NULL,
    priority                BIGINT       NOT NULL,
    user_regex              VARCHAR(512),
    original_user_regex     VARCHAR(512),
    authenticated_user_regex VARCHAR(512),
    user_group_regex        VARCHAR(512),
    source_regex            VARCHAR(512),
    query_type              VARCHAR(128),
    client_tags             TEXT,
    CONSTRAINT selectors_resource_group_id_fkey FOREIGN KEY (resource_group_id) REFERENCES resource_groups (resource_group_id) ON DELETE CASCADE) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
CREATE TABLE resource_groups_global_properties (name  VARCHAR(128) PRIMARY KEY,
    value VARCHAR(512)) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
CREATE TABLE trino_resource_group_revisions (id         CHAR(36) PRIMARY KEY DEFAULT (UUID()),
    engine_id  CHAR(36)        NOT NULL,
    revision   INTEGER     NOT NULL,
    config     JSON       NOT NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    UNIQUE (engine_id, revision),
    CONSTRAINT trino_resource_group_revisions_engine_id_fkey FOREIGN KEY (engine_id) REFERENCES engines (id) ON DELETE CASCADE) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`

const down00071Postgres = `
REVOKE SELECT ON resource_groups_global_properties FROM baleia_trino;
REVOKE SELECT ON selectors FROM baleia_trino;
REVOKE SELECT ON resource_groups FROM baleia_trino;
DROP TABLE IF EXISTS trino_resource_group_revisions;
DROP TABLE IF EXISTS resource_groups_global_properties;
DROP TABLE IF EXISTS selectors;
DROP TABLE IF EXISTS resource_groups;
`

const down00071MySQL = `DROP TABLE IF EXISTS trino_resource_group_revisions;
DROP TABLE IF EXISTS resource_groups_global_properties;
DROP TABLE IF EXISTS selectors;
DROP TABLE IF EXISTS resource_groups;`

func up00071(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, up00071Postgres, up00071MySQL)
}

func down00071(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, down00071Postgres, down00071MySQL)
}
