package migrations

import (
	"context"

	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(up00042, down00042)
}

const up00042Postgres = `
-- Correlation search (#49): the audit viewer filters by request_id and trace_id.
CREATE INDEX idx_audit_events_request_id ON audit_events (request_id) WHERE request_id IS NOT NULL;
CREATE INDEX idx_audit_events_trace_id ON audit_events (trace_id) WHERE trace_id IS NOT NULL;
`


const up00042MySQL = `
CREATE INDEX idx_audit_events_request_id ON audit_events (request_id) WHERE request_id IS NOT NULL;
CREATE INDEX idx_audit_events_trace_id ON audit_events (trace_id) WHERE trace_id IS NOT NULL;
`


const down00042Postgres = `
DROP INDEX IF EXISTS idx_audit_events_trace_id;
DROP INDEX IF EXISTS idx_audit_events_request_id;
`


const down00042MySQL = `
DROP INDEX IF EXISTS idx_audit_events_trace_id;
DROP INDEX IF EXISTS idx_audit_events_request_id;
`


func up00042(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, up00042Postgres, up00042MySQL)
}

func down00042(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, down00042Postgres, down00042MySQL)
}
