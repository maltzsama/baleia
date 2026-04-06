-- name: CreateTeam :one
INSERT INTO teams (name, description, created_by)
VALUES ($1, $2, $3)
RETURNING *;

-- name: GetTeamByID :one
SELECT * FROM teams WHERE id = $1;

-- name: ListTeams :many
SELECT * FROM teams ORDER BY name;

-- name: UpdateTeam :one
UPDATE teams
SET name = $2, description = $3, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: AddTeamMember :one
INSERT INTO team_members (team_id, user_id, role)
VALUES ($1, $2, $3)
RETURNING *;

-- name: UpdateTeamMemberRole :exec
UPDATE team_members SET role = $3
WHERE team_id = $1 AND user_id = $2;

-- name: RemoveTeamMember :exec
DELETE FROM team_members WHERE team_id = $1 AND user_id = $2;

-- name: ListTeamMembers :many
SELECT tm.*, u.name AS user_name, u.email AS user_email
FROM team_members tm
JOIN users u ON u.id = tm.user_id
WHERE tm.team_id = $1
ORDER BY u.name;

-- name: ListTeamsByUser :many
SELECT t.*
FROM teams t
JOIN team_members tm ON tm.team_id = t.id
WHERE tm.user_id = $1
ORDER BY t.name;

-- name: CreateWorkspace :one
INSERT INTO workspaces (name, description, type, polaris_catalog_name, owner_user_id, owner_team_id)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetWorkspaceByID :one
SELECT * FROM workspaces WHERE id = $1;

-- name: GetWorkspaceByPolarisName :one
SELECT * FROM workspaces WHERE polaris_catalog_name = $1;

-- name: ListWorkspacesByUser :many
SELECT DISTINCT w.*
FROM workspaces w
LEFT JOIN team_members tm ON tm.team_id = w.owner_team_id
WHERE w.owner_user_id = $1
   OR tm.user_id = $1
ORDER BY w.name;

-- name: UpdateWorkspace :one
UPDATE workspaces
SET name = $2, description = $3, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: DeleteTeam :exec
DELETE FROM teams WHERE id = $1;
