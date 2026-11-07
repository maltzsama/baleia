package migrations

import (
	"context"

	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(up00003, down00003)
}

const up00003Postgres = `
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
`

const up00003MySQL = `CREATE TABLE catalog_connections (id               CHAR(36) PRIMARY KEY DEFAULT (UUID()),
    name             TEXT        NOT NULL,
    type             TEXT        NOT NULL,
    url              TEXT        NOT NULL,
    credentials_enc  TEXT,
    default_readonly BOOLEAN          NOT NULL DEFAULT true,
    created_at       DATETIME(6)   NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at       DATETIME(6)   NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    CONSTRAINT catalog_connections_name_key UNIQUE (name(255)),
    CONSTRAINT catalog_connections_type_check CHECK (type IN ('polaris', 'nessie', 'glue', 'hive'))) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
CREATE TABLE workspace_catalog_access (id                    CHAR(36) PRIMARY KEY DEFAULT (UUID()),
    workspace_id          CHAR(36)          NOT NULL,
    catalog_connection_id CHAR(36)          NOT NULL,
    permission            TEXT        NOT NULL,
    granted_by            CHAR(36)          NOT NULL,
    granted_at            DATETIME(6)   NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    UNIQUE (workspace_id, catalog_connection_id),
    CONSTRAINT workspace_catalog_access_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE CASCADE,
    CONSTRAINT workspace_catalog_access_catalog_connection_id_fkey FOREIGN KEY (catalog_connection_id) REFERENCES catalog_connections (id) ON DELETE CASCADE,
    CONSTRAINT workspace_catalog_access_permission_check CHECK (permission IN ('read', 'write')),
    CONSTRAINT workspace_catalog_access_granted_by_fkey FOREIGN KEY (granted_by) REFERENCES users (id) ON DELETE RESTRICT) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
CREATE INDEX IF NOT EXISTS idx_workspace_catalog_access_workspace ON workspace_catalog_access (workspace_id);
CREATE INDEX IF NOT EXISTS idx_workspace_catalog_access_catalog   ON workspace_catalog_access (catalog_connection_id);`

const down00003Postgres = `
DROP TABLE IF EXISTS workspace_catalog_access;
DROP TABLE IF EXISTS catalog_connections;
`

const down00003MySQL = `DROP TABLE IF EXISTS workspace_catalog_access;
DROP TABLE IF EXISTS catalog_connections;`

func up00003(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, up00003Postgres, up00003MySQL)
}

func down00003(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, down00003Postgres, down00003MySQL)
}
