package migrations

import (
	"context"

	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(up00055, down00055)
}

const up00055Postgres = `
-- Per-agent capability policy (#60). '{}' means "defaults"; the effective
-- policy is defaults ⊕ stored fields, clamped by the server ceilings.
ALTER TABLE agents
    ADD COLUMN policy            JSONB       NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN policy_updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    ADD COLUMN policy_updated_by UUID        REFERENCES users (id) ON DELETE SET NULL;

-- Concurrency and hourly quotas are computed from durable executions (#60).
CREATE INDEX idx_query_executions_agent_started
    ON query_executions (agent_id, started_at DESC);
`

const up00055MySQL = `ALTER TABLE agents ADD COLUMN IF NOT EXISTS policy JSON       NOT NULL DEFAULT ('[]'),
    ADD COLUMN IF NOT EXISTS policy_updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    ADD COLUMN IF NOT EXISTS policy_updated_by CHAR(36),
    ADD CONSTRAINT agents_policy_updated_by_fkey FOREIGN KEY (policy_updated_by) REFERENCES users (id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS idx_query_executions_agent_started
    ON query_executions (agent_id, started_at DESC);`

const down00055Postgres = `
DROP INDEX IF EXISTS idx_query_executions_agent_started;
ALTER TABLE agents
    DROP COLUMN IF EXISTS policy_updated_by,
    DROP COLUMN IF EXISTS policy_updated_at,
    DROP COLUMN IF EXISTS policy;
`

const down00055MySQL = `DROP INDEX IF EXISTS idx_query_executions_agent_started;
ALTER TABLE agents DROP COLUMN IF EXISTS policy_updated_by,
    DROP COLUMN IF EXISTS policy_updated_at,
    DROP COLUMN IF EXISTS policy;`

func up00055(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, up00055Postgres, up00055MySQL)
}

func down00055(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, down00055Postgres, down00055MySQL)
}
