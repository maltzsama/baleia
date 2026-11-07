package migrations

import (
	"context"

	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(up00031, down00031)
}

const up00031Postgres = `
-- One activity runs every statement of an execution in one engine session
-- (#27): statements are created 'pending' and become 'running' one at a time;
-- after a failure, a cancel or a crash the rest are 'skipped'. error_code is
-- the typed engine code of a failed statement (queryengine.ErrorCode, or
-- 'interrupted' when the worker died mid-statement).
ALTER TABLE query_statements DROP CONSTRAINT query_statements_status_check;
ALTER TABLE query_statements ADD CONSTRAINT query_statements_status_check
    CHECK (status IN ('pending', 'running', 'finished', 'failed', 'cancelled', 'skipped'));
ALTER TABLE query_statements ADD COLUMN error_code TEXT;
`


const up00031MySQL = `
ALTER TABLE query_statements DROP CONSTRAINT query_statements_status_check;
ALTER TABLE query_statements ADD CONSTRAINT query_statements_status_check
    CHECK (status IN ('pending', 'running', 'finished', 'failed', 'cancelled', 'skipped'));
ALTER TABLE query_statements ADD COLUMN error_code TEXT;
`


const down00031Postgres = `
UPDATE query_statements SET status = 'cancelled' WHERE status IN ('pending', 'skipped');
ALTER TABLE query_statements DROP COLUMN IF EXISTS error_code;
ALTER TABLE query_statements DROP CONSTRAINT query_statements_status_check;
ALTER TABLE query_statements ADD CONSTRAINT query_statements_status_check
    CHECK (status IN ('running', 'finished', 'failed', 'cancelled'));
`


const down00031MySQL = `
ALTER TABLE query_statements DROP COLUMN error_code;
ALTER TABLE query_statements DROP CONSTRAINT query_statements_status_check;
ALTER TABLE query_statements ADD CONSTRAINT query_statements_status_check
    CHECK (status IN ('running', 'finished', 'failed', 'cancelled'));
`


func up00031(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, up00031Postgres, up00031MySQL)
}

func down00031(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, down00031Postgres, down00031MySQL)
}
