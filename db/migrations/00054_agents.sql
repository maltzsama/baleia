-- +goose Up

-- Agents are non-human principals that act on behalf of a user (#59). They are
-- bound to workspaces, are always read-only, and authenticate only with a
-- `bal_agt_` token on /mcp. Assistant agents (kind='assistant') are managed by
-- #62 and never listed by the agents API.
CREATE TABLE agents (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_id    UUID        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    kind        TEXT        NOT NULL DEFAULT 'external' CHECK (kind IN ('external', 'assistant')),
    name        TEXT        NOT NULL,
    description TEXT        NOT NULL DEFAULT '',
    status      TEXT        NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'disabled')),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_agents_owner ON agents (owner_id);
CREATE INDEX idx_agents_kind  ON agents (kind);

-- The workspaces an agent may act in. Rights are the owner's member rights
-- intersected with this set; the binding is re-checked on every call.
CREATE TABLE agent_workspaces (
    agent_id     UUID NOT NULL REFERENCES agents (id) ON DELETE CASCADE,
    workspace_id UUID NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    PRIMARY KEY (agent_id, workspace_id)
);

CREATE INDEX idx_agent_workspaces_workspace ON agent_workspaces (workspace_id);

CREATE TABLE agent_tokens (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    agent_id     UUID        NOT NULL REFERENCES agents (id) ON DELETE CASCADE,
    name         TEXT        NOT NULL,
    token_hash   TEXT        NOT NULL UNIQUE,
    prefix       TEXT        NOT NULL,
    expires_at   TIMESTAMPTZ NOT NULL,
    last_used_at TIMESTAMPTZ,
    created_by   UUID        NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at   TIMESTAMPTZ
);

CREATE INDEX idx_agent_tokens_agent ON agent_tokens (agent_id);

-- Agent-caused executions are attributable and quota-counted (#59, #60).
ALTER TABLE query_executions
    ADD COLUMN agent_id UUID REFERENCES agents (id) ON DELETE SET NULL;

CREATE INDEX idx_query_executions_agent ON query_executions (agent_id);

-- +goose Down

ALTER TABLE query_executions DROP COLUMN IF EXISTS agent_id;
DROP TABLE IF EXISTS agent_tokens;
DROP TABLE IF EXISTS agent_workspaces;
DROP TABLE IF EXISTS agents;
