-- +goose Up

-- Per-agent capability policy (#60). `{}` means "defaults"; the effective
-- policy is defaults ⊕ stored fields, clamped by the server ceilings.
ALTER TABLE agents
    ADD COLUMN policy            JSONB       NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN policy_updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    ADD COLUMN policy_updated_by UUID        REFERENCES users (id) ON DELETE SET NULL;

-- Concurrency and hourly quotas are computed from durable executions (#60).
CREATE INDEX idx_query_executions_agent_started
    ON query_executions (agent_id, started_at DESC);

-- +goose Down

DROP INDEX IF EXISTS idx_query_executions_agent_started;
ALTER TABLE agents
    DROP COLUMN IF EXISTS policy_updated_by,
    DROP COLUMN IF EXISTS policy_updated_at,
    DROP COLUMN IF EXISTS policy;
