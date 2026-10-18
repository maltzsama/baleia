-- Agents (#59). External agents only; assistant agents are managed by #62.

-- name: CreateAgent :one
INSERT INTO agents (owner_id, kind, name, description)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetAgentByID :one
SELECT * FROM agents WHERE id = $1;

-- name: ListAgentsByOwner :many
SELECT * FROM agents
WHERE owner_id = $1 AND kind = 'external'
ORDER BY created_at DESC;

-- name: ListAllAgents :many
SELECT * FROM agents WHERE kind = 'external' ORDER BY created_at DESC;

-- name: UpdateAgent :one
UPDATE agents
SET name = $2, description = $3, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: SetAgentStatus :one
UPDATE agents
SET status = $2, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: DeleteAgent :exec
DELETE FROM agents WHERE id = $1;

-- name: AddAgentWorkspace :exec
INSERT INTO agent_workspaces (agent_id, workspace_id)
VALUES ($1, $2)
ON CONFLICT DO NOTHING;

-- name: ClearAgentWorkspaces :exec
DELETE FROM agent_workspaces WHERE agent_id = $1;

-- name: ListAgentWorkspaces :many
SELECT w.* FROM workspaces w
JOIN agent_workspaces aw ON aw.workspace_id = w.id
WHERE aw.agent_id = $1
ORDER BY w.name;

-- name: AgentWorkspaceBound :one
SELECT EXISTS (
    SELECT 1 FROM agent_workspaces WHERE agent_id = $1 AND workspace_id = $2
) AS bound;

-- name: CreateAgentToken :one
INSERT INTO agent_tokens (agent_id, name, token_hash, prefix, expires_at, created_by)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetAgentTokenByHash :one
SELECT * FROM agent_tokens WHERE token_hash = $1;

-- name: GetAgentTokenByID :one
SELECT * FROM agent_tokens WHERE id = $1 AND agent_id = $2;

-- name: ListAgentTokensByAgent :many
SELECT * FROM agent_tokens WHERE agent_id = $1 ORDER BY created_at DESC;

-- name: TouchAgentToken :exec
UPDATE agent_tokens SET last_used_at = now() WHERE id = $1;

-- name: RevokeAgentToken :exec
UPDATE agent_tokens
SET revoked_at = now()
WHERE id = $1 AND agent_id = $2 AND revoked_at IS NULL;

-- name: CountActiveAgentTokens :one
SELECT count(*) FROM agent_tokens
WHERE agent_id = $1 AND revoked_at IS NULL AND expires_at > now();
