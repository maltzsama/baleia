package migrations

import (
	"context"

	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(up00022, down00022)
}

const up00022Postgres = `
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
`

const up00022MySQL = `CREATE TABLE workspace_catalog_grants (workspace_id CHAR(36)        NOT NULL,
    catalog_id   CHAR(36)        NOT NULL,
    permission   TEXT        NOT NULL,
    granted_by   CHAR(36)        NOT NULL,
    granted_at   DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (workspace_id, catalog_id),
    CONSTRAINT workspace_catalog_grants_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE CASCADE,
    CONSTRAINT workspace_catalog_grants_catalog_id_fkey FOREIGN KEY (catalog_id) REFERENCES catalogs (id)   ON DELETE CASCADE,
    CONSTRAINT workspace_catalog_grants_permission_check CHECK (permission IN ('read', 'write')),
    CONSTRAINT workspace_catalog_grants_granted_by_fkey FOREIGN KEY (granted_by) REFERENCES users (id)      ON DELETE RESTRICT) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
CREATE INDEX IF NOT EXISTS idx_workspace_catalog_grants_catalog ON workspace_catalog_grants (catalog_id);`

const down00022Postgres = `
DROP TABLE IF EXISTS workspace_catalog_grants;
`

const down00022MySQL = `DROP TABLE IF EXISTS workspace_catalog_grants;`

func up00022(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, up00022Postgres, up00022MySQL)
}

func down00022(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, down00022Postgres, down00022MySQL)
}
