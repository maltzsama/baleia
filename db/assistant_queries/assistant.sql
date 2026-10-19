-- Assistant settings, conversations, messages and the assistant agent (#62).
-- This file is the only source of the assistant sqlc package; the service may
-- not use the main db package for data access.

-- name: GetAssistantSettings :one
SELECT * FROM assistant_settings WHERE id;

-- name: UpdateAssistantSettings :one
UPDATE assistant_settings SET
    enabled                     = $1,
    provider                    = $2,
    base_url                    = $3,
    model                       = $4,
    api_key_enc                 = $5,
    data_sharing                = $6,
    max_turn_seconds            = $7,
    max_tool_calls_per_turn     = $8,
    max_output_tokens_per_turn  = $9,
    daily_token_budget_per_user = $10,
    updated_by                  = $11,
    updated_at                  = now()
WHERE id
RETURNING *;

-- name: FindAssistantAgent :one
-- One assistant agent per owner × workspace, provisioned on first use.
SELECT a.*
FROM agents a
JOIN agent_workspaces aw ON aw.agent_id = a.id
WHERE a.owner_id = $1 AND a.kind = 'assistant' AND aw.workspace_id = $2
LIMIT 1;

-- name: CreateAssistantAgent :one
INSERT INTO agents (owner_id, kind, name, description)
VALUES ($1, 'assistant', $2, $3)
RETURNING *;

-- name: AddAssistantAgentWorkspace :exec
INSERT INTO agent_workspaces (agent_id, workspace_id)
VALUES ($1, $2)
ON CONFLICT DO NOTHING;

-- name: CreateConversation :one
INSERT INTO assistant_conversations (user_id, workspace_id, agent_id, title)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetConversation :one
SELECT * FROM assistant_conversations WHERE id = $1 AND user_id = $2;

-- name: ListConversations :many
SELECT * FROM assistant_conversations
WHERE user_id = $1 AND workspace_id = $2
ORDER BY updated_at DESC, id DESC
LIMIT $3;

-- name: DeleteConversation :exec
DELETE FROM assistant_conversations WHERE id = $1 AND user_id = $2;

-- name: ClaimConversation :one
-- Exactly one turn may run per conversation. Zero rows means a turn is running.
UPDATE assistant_conversations
SET state = 'running', turn_owner = $3, turn_heartbeat_at = now(), updated_at = now()
WHERE id = $1 AND user_id = $2 AND state <> 'running'
RETURNING *;

-- name: ReleaseConversation :exec
UPDATE assistant_conversations
SET state = 'idle', turn_owner = NULL, turn_heartbeat_at = NULL, updated_at = now()
WHERE id = $1;

-- name: SetConversationInterrupted :exec
UPDATE assistant_conversations
SET state = 'interrupted', turn_owner = NULL, turn_heartbeat_at = NULL, updated_at = now()
WHERE id = $1;

-- name: HeartbeatConversation :exec
UPDATE assistant_conversations
SET turn_heartbeat_at = now()
WHERE id = $1 AND state = 'running';

-- name: InterruptStaleConversations :many
-- A crashed replica leaves a running turn behind; the sweeper marks it
-- interrupted so the user can retry.
UPDATE assistant_conversations
SET state = 'interrupted', turn_owner = NULL, turn_heartbeat_at = NULL, updated_at = now()
WHERE state = 'running' AND turn_heartbeat_at < now() - interval '60 seconds'
RETURNING id;

-- name: SetConversationTitle :exec
UPDATE assistant_conversations SET title = $2, updated_at = now()
WHERE id = $1 AND title = '';

-- name: AppendMessage :one
-- Bumps last_seq and inserts atomically, so SSE sequence numbers have no gaps.
WITH next AS (
    UPDATE assistant_conversations
    SET last_seq = last_seq + 1, updated_at = now()
    WHERE id = $1
    RETURNING last_seq
)
INSERT INTO assistant_messages (conversation_id, seq, role, content, input_tokens, output_tokens)
SELECT $1, next.last_seq, $2, $3, $4, $5 FROM next
RETURNING *;

-- name: ListMessages :many
SELECT * FROM assistant_messages
WHERE conversation_id = $1 AND seq > $2
ORDER BY seq
LIMIT $3;

-- name: CountUserTokensSince :one
SELECT COALESCE(SUM(m.input_tokens + m.output_tokens), 0)::bigint
FROM assistant_messages m
JOIN assistant_conversations c ON c.id = m.conversation_id
WHERE c.user_id = $1 AND m.created_at >= $2;

-- name: LatestConversationMessage :one
SELECT * FROM assistant_messages
WHERE conversation_id = $1
ORDER BY seq DESC
LIMIT 1;
