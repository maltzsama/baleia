package migrations

import (
	"context"

	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(up00046, down00046)
}

const up00046Postgres = `
-- Every refresh token belongs to a rotation family (#51). The first token of a
-- family uses its own id as family_id; rotated tokens inherit it so reuse
-- detection can revoke the whole family. Rows that predate the column are
-- backfilled to their own id, which is equivalent to one family per token.
ALTER TABLE refresh_tokens ADD COLUMN family_id UUID;
UPDATE refresh_tokens SET family_id = id WHERE family_id IS NULL;
ALTER TABLE refresh_tokens ALTER COLUMN family_id SET NOT NULL;

CREATE INDEX idx_refresh_tokens_family_id ON refresh_tokens(family_id);
`


const up00046MySQL = `
ALTER TABLE refresh_tokens ADD COLUMN family_id CHAR(36);

ALTER TABLE refresh_tokens ALTER COLUMN family_id SET NOT NULL;

CREATE INDEX idx_refresh_tokens_family_id ON refresh_tokens(family_id);
`


const down00046Postgres = `
DROP INDEX IF EXISTS idx_refresh_tokens_family_id;
ALTER TABLE refresh_tokens DROP COLUMN IF EXISTS family_id;
`


const down00046MySQL = `
DROP INDEX IF EXISTS idx_refresh_tokens_family_id;
ALTER TABLE refresh_tokens DROP COLUMN family_id;
`


func up00046(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, up00046Postgres, up00046MySQL)
}

func down00046(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, down00046Postgres, down00046MySQL)
}
