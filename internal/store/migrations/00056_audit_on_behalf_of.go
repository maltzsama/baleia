package migrations

import (
	"context"

	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(up00056, down00056)
}

const up00056Postgres = `
-- The owner an agent acts for (#61). NULL for human and service actors; set
-- for every event caused by an agent. ALTER TABLE on the partitioned parent
-- propagates to the existing partitions.
ALTER TABLE audit_events ADD COLUMN on_behalf_of UUID;

CREATE INDEX audit_events_on_behalf_of
    ON audit_events (on_behalf_of, occurred_at DESC)
    WHERE on_behalf_of IS NOT NULL;
`


const up00056MySQL = `
ALTER TABLE audit_events ADD COLUMN on_behalf_of CHAR(36);

CREATE INDEX audit_events_on_behalf_of
    ON audit_events (on_behalf_of, occurred_at DESC)
    WHERE on_behalf_of IS NOT NULL;
`


const down00056Postgres = `
DROP INDEX IF EXISTS audit_events_on_behalf_of;
ALTER TABLE audit_events DROP COLUMN IF EXISTS on_behalf_of;
`


const down00056MySQL = `
DROP INDEX IF EXISTS audit_events_on_behalf_of;
ALTER TABLE audit_events DROP COLUMN on_behalf_of;
`


func up00056(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, up00056Postgres, up00056MySQL)
}

func down00056(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, down00056Postgres, down00056MySQL)
}
