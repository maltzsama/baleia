package migrations

import (
	"context"

	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(up00070, down00070)
}

const up00070Postgres = `
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
`

const up00070MySQL = `CREATE TABLE table_popularity (workspace_id CHAR(36)             NOT NULL,
    catalog_id   CHAR(36)             NOT NULL,
    namespace    TEXT             NOT NULL DEFAULT (''),
    table_name   TEXT             NOT NULL,
    uses         BIGINT           NOT NULL DEFAULT 0,
    score        DOUBLE PRECISION NOT NULL DEFAULT 0,
    last_used_at DATETIME(6)      NOT NULL,
    computed_at  DATETIME(6)      NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (workspace_id, catalog_id, namespace(255), table_name(255)),
    CONSTRAINT table_popularity_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE CASCADE,
    CONSTRAINT table_popularity_catalog_id_fkey FOREIGN KEY (catalog_id) REFERENCES catalogs (id) ON DELETE CASCADE) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
CREATE INDEX IF NOT EXISTS table_popularity_workspace ON table_popularity (workspace_id, score DESC);`

const down00070Postgres = `
DROP TABLE IF EXISTS table_popularity;
`

const down00070MySQL = `DROP TABLE IF EXISTS table_popularity;`

func up00070(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, up00070Postgres, up00070MySQL)
}

func down00070(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, down00070Postgres, down00070MySQL)
}
