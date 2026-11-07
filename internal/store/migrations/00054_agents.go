package migrations

import (
	"context"

	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(up00054, down00054)
}

const up00054Postgres = `
-- Agents are non-human principals that act on behalf of a user (#59). They are
-- bound to workspaces, are always read-only, and authenticate only with a
-- 'bal_agt_' token on /mcp. Assistant agents (kind='assistant') are managed by
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
`

const up00054MySQL = `CREATE TABLE agents (id          CHAR(36) PRIMARY KEY DEFAULT (UUID()),
    owner_id    CHAR(36)        NOT NULL,
    kind        TEXT        NOT NULL DEFAULT ('external'),
    name        TEXT        NOT NULL,
    description TEXT        NOT NULL DEFAULT (''),
    status      TEXT        NOT NULL DEFAULT ('active'),
    created_at  DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at  DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    CONSTRAINT agents_owner_id_fkey FOREIGN KEY (owner_id) REFERENCES users (id) ON DELETE CASCADE,
    CONSTRAINT agents_kind_check CHECK (kind IN ('external', 'assistant')),
    CONSTRAINT agents_status_check CHECK (status IN ('active', 'disabled'))) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
CREATE INDEX IF NOT EXISTS idx_agents_owner ON agents (owner_id);
CREATE INDEX IF NOT EXISTS idx_agents_kind  ON agents (kind(255));
CREATE TABLE agent_workspaces (agent_id     CHAR(36) NOT NULL,
    workspace_id CHAR(36) NOT NULL,
    PRIMARY KEY (agent_id, workspace_id),
    CONSTRAINT agent_workspaces_agent_id_fkey FOREIGN KEY (agent_id) REFERENCES agents (id) ON DELETE CASCADE,
    CONSTRAINT agent_workspaces_workspace_id_fkey FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE CASCADE) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
CREATE INDEX IF NOT EXISTS idx_agent_workspaces_workspace ON agent_workspaces (workspace_id);
CREATE TABLE agent_tokens (id           CHAR(36) PRIMARY KEY DEFAULT (UUID()),
    agent_id     CHAR(36)        NOT NULL,
    name         TEXT        NOT NULL,
    token_hash   TEXT        NOT NULL,
    prefix       TEXT        NOT NULL,
    expires_at   DATETIME(6) NOT NULL,
    last_used_at DATETIME(6),
    created_by   CHAR(36)        NOT NULL,
    created_at   DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    revoked_at   DATETIME(6),
    CONSTRAINT agent_tokens_agent_id_fkey FOREIGN KEY (agent_id) REFERENCES agents (id) ON DELETE CASCADE,
    CONSTRAINT agent_tokens_token_hash_key UNIQUE (token_hash(255)),
    CONSTRAINT agent_tokens_created_by_fkey FOREIGN KEY (created_by) REFERENCES users (id) ON DELETE RESTRICT) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
CREATE INDEX IF NOT EXISTS idx_agent_tokens_agent ON agent_tokens (agent_id);
ALTER TABLE query_executions ADD COLUMN IF NOT EXISTS agent_id CHAR(36),
    ADD CONSTRAINT query_executions_agent_id_fkey FOREIGN KEY (agent_id) REFERENCES agents (id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS idx_query_executions_agent ON query_executions (agent_id);`

const down00054Postgres = `
ALTER TABLE query_executions DROP COLUMN IF EXISTS agent_id;
DROP TABLE IF EXISTS agent_tokens;
DROP TABLE IF EXISTS agent_workspaces;
DROP TABLE IF EXISTS agents;
`

const down00054MySQL = `ALTER TABLE query_executions DROP COLUMN IF EXISTS agent_id;
DROP TABLE IF EXISTS agent_tokens;
DROP TABLE IF EXISTS agent_workspaces;
DROP TABLE IF EXISTS agents;`

func up00054(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, up00054Postgres, up00054MySQL)
}

func down00054(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, down00054Postgres, down00054MySQL)
}
