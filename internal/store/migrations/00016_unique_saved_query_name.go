package migrations

import (
	"context"

	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(up00016, down00016)
}

const up00016Postgres = `
DELETE FROM saved_queries sq1 USING saved_queries sq2
WHERE sq1.created_at < sq2.created_at
  AND sq1.workspace_id = sq2.workspace_id
  AND sq1.name = sq2.name;

CREATE UNIQUE INDEX idx_saved_queries_unique_name ON saved_queries (workspace_id, name);
`

const up00016MySQL = `CREATE UNIQUE INDEX IF NOT EXISTS idx_saved_queries_unique_name ON saved_queries (workspace_id, name(255));`

const down00016Postgres = `
DROP INDEX IF EXISTS idx_saved_queries_unique_name;
`

const down00016MySQL = `DROP INDEX IF EXISTS idx_saved_queries_unique_name;`

func up00016(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, up00016Postgres, up00016MySQL)
}

func down00016(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, down00016Postgres, down00016MySQL)
}
