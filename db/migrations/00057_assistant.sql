-- +goose Up

-- Assistant configuration (#62). One row; the API key is sealed with the
-- Keyring under AAD "assistant:api_key" (#19) and never returned.
CREATE TABLE assistant_settings (
    id                        BOOLEAN PRIMARY KEY DEFAULT true CHECK (id),
    enabled                   BOOLEAN NOT NULL DEFAULT false,
    provider                  TEXT CHECK (provider IN ('anthropic', 'openai_compatible')),
    base_url                  TEXT,
    model                     TEXT,
    api_key_enc               TEXT,
    data_sharing              TEXT NOT NULL DEFAULT 'metadata_only'
                              CHECK (data_sharing IN ('metadata_only', 'metadata_and_samples')),
    max_turn_seconds          INTEGER NOT NULL DEFAULT 180,
    max_tool_calls_per_turn   INTEGER NOT NULL DEFAULT 10,
    max_output_tokens_per_turn INTEGER NOT NULL DEFAULT 4096,
    daily_token_budget_per_user BIGINT NOT NULL DEFAULT 500000,
    updated_by                UUID REFERENCES users (id) ON DELETE SET NULL,
    updated_at                TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO assistant_settings DEFAULT VALUES;

-- A conversation belongs to one user and one workspace; the assistant agent is
-- provisioned per user × workspace and owns the tool calls.
CREATE TABLE assistant_conversations (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id           UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    workspace_id      UUID NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    agent_id          UUID NOT NULL REFERENCES agents (id) ON DELETE CASCADE,
    title             TEXT NOT NULL DEFAULT '',
    state             TEXT NOT NULL DEFAULT 'idle'
                      CHECK (state IN ('idle', 'running', 'interrupted')),
    turn_owner        TEXT,
    turn_heartbeat_at TIMESTAMPTZ,
    last_seq          INTEGER NOT NULL DEFAULT 0,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX assistant_conversations_user ON assistant_conversations (user_id, updated_at DESC);

-- History is append-only and keyed by a per-conversation sequence so SSE
-- clients can replay with after_seq. content is typed per role by the service.
CREATE TABLE assistant_messages (
    conversation_id UUID NOT NULL REFERENCES assistant_conversations (id) ON DELETE CASCADE,
    seq             INTEGER NOT NULL,
    role            TEXT NOT NULL
                    CHECK (role IN ('user', 'assistant', 'tool_call', 'tool_result', 'notice')),
    content         JSONB NOT NULL,
    input_tokens    INTEGER,
    output_tokens   INTEGER,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (conversation_id, seq)
);

CREATE INDEX assistant_messages_usage ON assistant_messages (created_at)
    WHERE output_tokens IS NOT NULL;

-- +goose Down

DROP TABLE IF EXISTS assistant_messages;
DROP TABLE IF EXISTS assistant_conversations;
DROP TABLE IF EXISTS assistant_settings;
