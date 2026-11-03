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

-- name: UpdateTeamMemberRole :one
UPDATE team_members SET role = $3
WHERE team_id = $1 AND user_id = $2
RETURNING *;

-- name: UpsertTeamMember :exec
INSERT INTO team_members (team_id, user_id, role)
VALUES ($1, $2, $3)
ON CONFLICT (team_id, user_id) DO UPDATE SET role = EXCLUDED.role;

-- name: RemoveTeamMember :execrows
DELETE FROM team_members WHERE team_id = $1 AND user_id = $2;

-- name: ListTeamMembers :many
SELECT tm.*, u.name AS user_name, u.email AS user_email,
       u.role AS user_role, u.auth_provider AS user_auth_provider,
       u.status AS user_status, u.created_at AS user_created_at
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
-- The workspaces a user is a member of (#653): their personal workspace,
-- personal workspaces shared with them and their teams' workspaces.
SELECT w.*
FROM workspaces w
WHERE baleia_workspace_member_role(w.id, @user_id::uuid) IS NOT NULL
ORDER BY w.name;

-- name: UpdateWorkspace :one
UPDATE workspaces
SET name = $2, description = $3, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: CheckWorkspaceAccess :one
SELECT (baleia_workspace_member_role(@id::uuid, @user_id::uuid) IS NOT NULL)::bool AS has_access;

-- name: GetWorkspaceMemberRole :one
-- The user's role in the workspace (#653): admin, editor or viewer; empty for
-- a non-member. A global admin is not a member by this query.
SELECT COALESCE(baleia_workspace_member_role(@id::uuid, @user_id::uuid), '')::text AS role;

-- name: SharesWorkspace :one
-- Whether two users are both members of one workspace, by the membership
-- rule of CheckWorkspaceAccess (#432).
SELECT EXISTS (
    SELECT 1 FROM workspaces w
    WHERE baleia_workspace_member_role(w.id, @caller_id::uuid) IS NOT NULL
      AND baleia_workspace_member_role(w.id, @user_id::uuid) IS NOT NULL
) AS shares;

-- name: DeleteTeam :exec
DELETE FROM teams WHERE id = $1;

-- name: IsWorkspaceOwner :one
-- Whether the user is an admin of the workspace (#653): the owner of a
-- personal one, and a team owner of a shared one unless another role was set.
SELECT (baleia_workspace_member_role(@id::uuid, @user_id::uuid) = 'admin')::bool AS is_owner;

-- name: GetTeamMemberRole :one
SELECT role FROM team_members WHERE team_id = $1 AND user_id = $2;

-- name: UpsertWorkspaceCatalogGrant :one
INSERT INTO workspace_catalog_grants (workspace_id, catalog_id, granted_by)
VALUES ($1, $2, $3)
ON CONFLICT (workspace_id, catalog_id) DO UPDATE
SET granted_by = EXCLUDED.granted_by, granted_at = now()
RETURNING *;

-- name: ListWorkspaceCatalogGrants :many
-- catalog_name is the Trino name of a source catalog (what the UI showed
-- before bindings) and the logical name of a native one.
SELECT g.workspace_id, g.catalog_id, g.granted_at, COALESCE(c.trino_catalog_name, c.name)::text AS catalog_name
FROM workspace_catalog_grants g
JOIN catalogs c ON c.id = g.catalog_id
WHERE g.workspace_id = $1
ORDER BY 4;

-- name: DeleteWorkspaceCatalogGrant :execrows
DELETE FROM workspace_catalog_grants WHERE workspace_id = $1 AND catalog_id = $2;

-- name: CatalogGrantedToUser :one
-- True when the user is a member of a workspace that has been granted the catalog.
SELECT EXISTS (
    SELECT 1 FROM workspace_catalog_grants g
    WHERE g.catalog_id = @catalog_id::uuid
      AND baleia_workspace_member_role(g.workspace_id, @user_id::uuid) IS NOT NULL
) AS granted;

-- name: CatalogGrantedToAgent :one
-- True when the catalog is granted to a workspace the agent is bound to and the
-- agent's owner is still a member of that workspace (#59).
SELECT EXISTS (
    SELECT 1 FROM workspace_catalog_grants g
    JOIN agent_workspaces aw ON aw.workspace_id = g.workspace_id AND aw.agent_id = @agent_id::uuid
    WHERE g.catalog_id = @catalog_id::uuid
      AND baleia_workspace_member_role(g.workspace_id, @user_id::uuid) IS NOT NULL
) AS granted;

-- name: ListCatalogsGrantedToUser :many
-- The catalogs of one connection that a user reaches through workspace grants.
SELECT DISTINCT c.* FROM catalogs c
JOIN workspace_catalog_grants g ON g.catalog_id = c.id
WHERE c.connection_id = @connection_id::uuid
  AND baleia_workspace_member_role(g.workspace_id, @user_id::uuid) IS NOT NULL;

-- name: ListGrantedCatalogsOfWorkspace :many
SELECT c.*, cc.name AS connection_name, cc.type AS connection_type
FROM workspace_catalog_grants g
JOIN catalogs c ON c.id = g.catalog_id
JOIN catalog_connections cc ON cc.id = c.connection_id
WHERE g.workspace_id = $1 AND c.enabled
ORDER BY c.trino_catalog_name;

-- name: PersonalWorkspaceID :one
SELECT id FROM workspaces WHERE type = 'personal' AND owner_user_id = $1 ORDER BY created_at LIMIT 1;

-- name: UserOwnsTeam :one
SELECT EXISTS (SELECT 1 FROM team_members WHERE user_id = $1 AND role = 'owner') AS owns;

-- name: ListWorkspacePrincipalUsers :many
-- Users who are members of the workspace (#113, #653).
SELECT u.id, u.name, u.email
FROM users u
WHERE baleia_workspace_member_role(@workspace_id::uuid, u.id) IS NOT NULL
ORDER BY u.name
LIMIT @lim::int;

-- name: DeleteWorkspace :execrows
DELETE FROM workspaces WHERE id = $1 AND type = 'shared';

-- name: CountWorkspaceRunningExecutions :one
SELECT count(*)::bigint FROM query_executions WHERE workspace_id = $1 AND status = 'running';

-- name: CountWorkspaceImpact :one
SELECT
    (SELECT count(*) FROM saved_queries WHERE workspace_id = @workspace_id::uuid)::bigint AS saved_queries,
    (SELECT count(*) FROM semantic_metrics WHERE workspace_id = @workspace_id::uuid)::bigint AS metrics,
    (SELECT count(*) FROM spaces WHERE workspace_id = @workspace_id::uuid)::bigint AS spaces,
    (SELECT count(*) FROM query_executions WHERE workspace_id = @workspace_id::uuid AND status = 'running')::bigint AS running_executions,
    EXISTS (SELECT 1 FROM workspaces w WHERE w.id = @workspace_id::uuid AND w.default_binding_id IS NOT NULL) AS default_target_affected;

-- name: CountCatalogAccessImpact :one
SELECT
    (SELECT count(*) FROM saved_queries s
      JOIN catalog_bindings b ON b.id = s.target_binding_id
      WHERE s.workspace_id = @workspace_id::uuid AND b.catalog_id = @catalog_id::uuid)::bigint AS saved_queries,
    (SELECT count(*) FROM semantic_metrics m
      WHERE m.workspace_id = @workspace_id::uuid AND m.catalog_id = @catalog_id::uuid)::bigint AS metrics,
    (SELECT count(*) FROM query_executions e
      JOIN catalog_bindings b ON b.id = e.target_binding_id
      WHERE e.workspace_id = @workspace_id::uuid AND b.catalog_id = @catalog_id::uuid AND e.status = 'running')::bigint AS running_executions,
    EXISTS (
      SELECT 1 FROM workspaces w
      JOIN catalog_bindings b ON b.id = w.default_binding_id
      WHERE w.id = @workspace_id::uuid AND b.catalog_id = @catalog_id::uuid
    ) AS default_target_affected;

-- name: ClearWorkspaceDefaultForCatalog :exec
UPDATE workspaces
SET default_binding_id = NULL, default_schema = NULL, updated_at = now()
WHERE id = @workspace_id::uuid
  AND default_binding_id IN (SELECT id FROM catalog_bindings WHERE catalog_id = @catalog_id::uuid);

-- name: ListWorkspaceMembers :many
-- The members of a workspace with their role (#653): source is how they are
-- a member (owner of a personal workspace, member of the owning team, or
-- shared with them), role_set whether their role was set explicitly.
SELECT sqlc.embed(u),
       baleia_workspace_member_role(w.id, u.id)::text AS role,
       (CASE WHEN w.owner_user_id = u.id THEN 'owner'
             WHEN w.type = 'shared' THEN 'team'
             ELSE 'shared' END)::text AS source,
       (wm.user_id IS NOT NULL)::bool AS role_set
FROM workspaces w
JOIN users u ON baleia_workspace_member_role(w.id, u.id) IS NOT NULL
LEFT JOIN workspace_members wm ON wm.workspace_id = w.id AND wm.user_id = u.id
WHERE w.id = @workspace_id::uuid
ORDER BY u.name;

-- name: UserInWorkspaceTeam :one
SELECT EXISTS (
    SELECT 1 FROM workspaces w
    JOIN team_members tm ON tm.team_id = w.owner_team_id AND tm.user_id = @user_id::uuid
    WHERE w.id = @workspace_id::uuid
) AS in_team;

-- name: UpsertWorkspaceMember :exec
INSERT INTO workspace_members (workspace_id, user_id, role, granted_by)
VALUES (@workspace_id::uuid, @user_id::uuid, @role::text, sqlc.narg(granted_by)::uuid)
ON CONFLICT (workspace_id, user_id) DO UPDATE
SET role = EXCLUDED.role, granted_by = EXCLUDED.granted_by, granted_at = now();

-- name: DeleteWorkspaceMember :execrows
DELETE FROM workspace_members WHERE workspace_id = @workspace_id::uuid AND user_id = @user_id::uuid;
