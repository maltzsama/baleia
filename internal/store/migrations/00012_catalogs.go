package migrations

import (
	"context"

	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(up00012, down00012)
}

const up00012Postgres = `
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
`

const up00012MySQL = `CREATE TABLE catalogs (id                 CHAR(36) PRIMARY KEY DEFAULT (UUID()),
    connection_id      CHAR(36) NOT NULL,
    name               TEXT NOT NULL,
    description        TEXT,
    trino_catalog_name TEXT NOT NULL,
    created_at         DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at         DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    UNIQUE (connection_id, name(255)),
    UNIQUE (trino_catalog_name(255)),
    CONSTRAINT catalogs_connection_id_fkey FOREIGN KEY (connection_id) REFERENCES catalog_connections(id) ON DELETE CASCADE) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
DROP TABLE IF EXISTS workspace_catalog_access;
CREATE TABLE workspace_catalog_access (id          CHAR(36) PRIMARY KEY DEFAULT (UUID()),
    workspace_id CHAR(36) NOT NULL,
    catalog_id   CHAR(36) NOT NULL,
    permission   TEXT NOT NULL DEFAULT ('read'),
    granted_by   CHAR(36) NOT NULL,
    granted_at   DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    UNIQUE (workspace_id, catalog_id),
    CONSTRAINT workspace_catalog_access_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE,
    CONSTRAINT workspace_catalog_access_catalog_id_fkey FOREIGN KEY (catalog_id) REFERENCES catalogs(id) ON DELETE CASCADE,
    CONSTRAINT workspace_catalog_access_granted_by_fkey FOREIGN KEY (granted_by) REFERENCES users(id)) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`

const down00012Postgres = `
DROP TABLE IF EXISTS workspace_catalog_access;
DROP TABLE IF EXISTS catalogs;
`

const down00012MySQL = `DROP TABLE IF EXISTS workspace_catalog_access;
DROP TABLE IF EXISTS catalogs;`

func up00012(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, up00012Postgres, up00012MySQL)
}

func down00012(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, down00012Postgres, down00012MySQL)
}
