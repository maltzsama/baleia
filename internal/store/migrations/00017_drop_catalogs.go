package migrations

import (
	"context"

	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(up00017, down00017)
}

const up00017Postgres = `
DROP TABLE IF EXISTS workspace_catalog_access;
DROP TABLE IF EXISTS catalogs;
`

const up00017MySQL = `
DROP TABLE IF EXISTS workspace_catalog_access;
DROP TABLE IF EXISTS catalogs;
`

const down00017Postgres = `
CREATE TABLE catalogs (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    connection_id      UUID NOT NULL REFERENCES catalog_connections(id) ON DELETE CASCADE,
    name               TEXT NOT NULL,
    description        TEXT,
    trino_catalog_name TEXT NOT NULL DEFAULT '',
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (connection_id, name)
);

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

const down00017MySQL = `
CREATE TABLE catalogs (
    id                 CHAR(36) PRIMARY KEY DEFAULT (UUID()),
    connection_id      CHAR(36) NOT NULL REFERENCES catalog_connections(id) ON DELETE CASCADE,
    name               TEXT NOT NULL,
    description        TEXT,
    trino_catalog_name TEXT NOT NULL DEFAULT '',
    created_at         DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at         DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    UNIQUE (connection_id, name)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE workspace_catalog_access (
    id          CHAR(36) PRIMARY KEY DEFAULT (UUID()),
    workspace_id CHAR(36) NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    catalog_id   CHAR(36) NOT NULL REFERENCES catalogs(id) ON DELETE CASCADE,
    permission   TEXT NOT NULL DEFAULT 'read',
    granted_by   CHAR(36) NOT NULL REFERENCES users(id),
    granted_at   DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    UNIQUE (workspace_id, catalog_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
`

func up00017(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, up00017Postgres, up00017MySQL)
}

func down00017(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, down00017Postgres, down00017MySQL)
}
