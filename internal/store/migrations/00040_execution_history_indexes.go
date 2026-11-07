package migrations

import (
	"context"

	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(up00040, down00040)
}

const up00040Postgres = `
-- Execution history (#36): a trigram index for substring search on SQL and a
-- composite index for the workspace's newest-first keyset pages.
CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE INDEX IF NOT EXISTS query_executions_sql_trgm
    ON query_executions USING gin (sql gin_trgm_ops);
CREATE INDEX IF NOT EXISTS query_executions_ws_started
    ON query_executions (workspace_id, started_at DESC, id DESC);
`


const up00040MySQL = `
CREATE INDEX query_executions_ws_started
    ON query_executions (workspace_id, started_at DESC, id DESC);
`


const down00040Postgres = `
DROP INDEX IF EXISTS query_executions_ws_started;
DROP INDEX IF EXISTS query_executions_sql_trgm;
`


const down00040MySQL = `
DROP INDEX IF EXISTS query_executions_ws_started;
DROP INDEX IF EXISTS query_executions_sql_trgm;
`


func up00040(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, up00040Postgres, up00040MySQL)
}

func down00040(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, down00040Postgres, down00040MySQL)
}
