-- +goose Up
-- #653: what a user may do in a workspace is their workspace role (admin,
-- editor, viewer); what they may read or write in a catalog stays with the
-- catalog manager (ADR 0015). Members of a shared workspace are its team's
-- members; a personal workspace is its owner's and whoever they share it with.
-- A row here sets a member's role; without one, a shared workspace keeps
-- today's abilities: team owner = admin, team member = editor.
CREATE TABLE workspace_members (
    workspace_id UUID        NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    user_id      UUID        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    role         TEXT        NOT NULL CHECK (role IN ('admin', 'editor', 'viewer')),
    granted_by   UUID        REFERENCES users (id) ON DELETE SET NULL,
    granted_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, user_id)
);
CREATE INDEX workspace_members_user ON workspace_members (user_id);

-- The caller's role in the workspace, NULL for a non-member. A global admin is
-- not a member by this function; authz adds that.
-- +goose StatementBegin
CREATE FUNCTION baleia_workspace_member_role(ws_id UUID, usr_id UUID)
RETURNS TEXT LANGUAGE sql STABLE AS $$
    SELECT CASE
        WHEN w.type = 'personal' AND w.owner_user_id = usr_id THEN 'admin'
        WHEN w.type = 'personal' THEN wm.role
        WHEN tm.user_id IS NULL THEN NULL
        ELSE COALESCE(wm.role, CASE tm.role WHEN 'owner' THEN 'admin' ELSE 'editor' END)
    END
    FROM workspaces w
    LEFT JOIN team_members tm ON tm.team_id = w.owner_team_id AND tm.user_id = usr_id
    LEFT JOIN workspace_members wm ON wm.workspace_id = w.id AND wm.user_id = usr_id
    WHERE w.id = ws_id
$$;
-- +goose StatementEnd

-- Space roles only narrow (#653): the effective role is the highest of the
-- space's grants (a workspace admin is a manager everywhere, a member gets the
-- space's default role when it is workspace-visible, explicit user/team
-- entries), capped by the workspace role: viewer -> viewer, editor -> editor,
-- admin -> manager. A global admin is a manager. A non-member has no role,
-- even with a stale entry. NULL means "invisible".
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION baleia_effective_space_role(ws_id UUID, sp_id UUID, usr_id UUID)
RETURNS TEXT LANGUAGE sql STABLE AS $$
    WITH space AS (
        SELECT s.* FROM spaces s WHERE s.id = sp_id AND s.workspace_id = ws_id
    ),
    ws_role AS (
        SELECT CASE
            WHEN EXISTS (SELECT 1 FROM users u WHERE u.id = usr_id AND u.role = 'admin') THEN 'admin'
            ELSE baleia_workspace_member_role(ws_id, usr_id)
        END AS role
    ),
    cap AS (
        SELECT CASE role WHEN 'admin' THEN 3 WHEN 'editor' THEN 2 WHEN 'viewer' THEN 1 END AS rank
        FROM ws_role WHERE role IS NOT NULL
    ),
    candidates AS (
        SELECT 3 AS rank WHERE EXISTS (SELECT 1 FROM ws_role WHERE role = 'admin')
        UNION ALL
        SELECT CASE (SELECT default_role FROM space) WHEN 'editor' THEN 2 ELSE 1 END
        WHERE EXISTS (SELECT 1 FROM space WHERE visibility = 'workspace')
        UNION ALL
        SELECT CASE sm.role WHEN 'viewer' THEN 1 WHEN 'editor' THEN 2 WHEN 'manager' THEN 3 END
        FROM space_members sm
        WHERE sm.space_id = sp_id AND sm.principal_type = 'user' AND sm.principal_id = usr_id
        UNION ALL
        SELECT CASE sm.role WHEN 'viewer' THEN 1 WHEN 'editor' THEN 2 WHEN 'manager' THEN 3 END
        FROM space_members sm
        JOIN team_members tm ON tm.team_id = sm.principal_id AND tm.user_id = usr_id
        WHERE sm.space_id = sp_id AND sm.principal_type = 'team'
    )
    SELECT CASE least(max(c.rank), (SELECT rank FROM cap))
        WHEN 3 THEN 'manager' WHEN 2 THEN 'editor' WHEN 1 THEN 'viewer' END
    FROM candidates c
    WHERE EXISTS (SELECT 1 FROM cap) AND EXISTS (SELECT 1 FROM space)
    HAVING max(c.rank) IS NOT NULL
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION baleia_effective_space_role(ws_id UUID, sp_id UUID, usr_id UUID)
RETURNS TEXT LANGUAGE sql STABLE AS $$
    WITH space AS (
        SELECT s.* FROM spaces s WHERE s.id = sp_id AND s.workspace_id = ws_id
    ),
    membership AS (
        SELECT
            -- A personal workspace is owned by its user; a shared one by the
            -- owners of its team (same rule as authz.IsWorkspaceOwner).
            (w.owner_user_id = usr_id) OR EXISTS (
                SELECT 1 FROM team_members tm
                WHERE tm.team_id = w.owner_team_id AND tm.user_id = usr_id AND tm.role = 'owner'
            ) AS is_owner,
            (w.owner_user_id = usr_id) OR EXISTS (
                SELECT 1 FROM team_members tm WHERE tm.team_id = w.owner_team_id AND tm.user_id = usr_id
            ) AS is_member
        FROM workspaces w WHERE w.id = ws_id
    ),
    candidates AS (
        SELECT 3 AS rank WHERE EXISTS (SELECT 1 FROM users u WHERE u.id = usr_id AND u.role = 'admin')
        UNION ALL
        SELECT 3 WHERE EXISTS (SELECT 1 FROM membership WHERE is_owner)
        UNION ALL
        SELECT CASE (SELECT default_role FROM space) WHEN 'editor' THEN 2 ELSE 1 END
        WHERE EXISTS (SELECT 1 FROM membership WHERE is_member)
          AND EXISTS (SELECT 1 FROM space WHERE visibility = 'workspace')
        UNION ALL
        SELECT CASE sm.role WHEN 'viewer' THEN 1 WHEN 'editor' THEN 2 WHEN 'manager' THEN 3 END
        FROM space_members sm
        WHERE EXISTS (SELECT 1 FROM membership WHERE is_member)
          AND sm.space_id = sp_id AND sm.principal_type = 'user' AND sm.principal_id = usr_id
        UNION ALL
        SELECT CASE sm.role WHEN 'viewer' THEN 1 WHEN 'editor' THEN 2 WHEN 'manager' THEN 3 END
        FROM space_members sm
        JOIN team_members tm ON tm.team_id = sm.principal_id AND tm.user_id = usr_id
        WHERE EXISTS (SELECT 1 FROM membership WHERE is_member)
          AND sm.space_id = sp_id AND sm.principal_type = 'team'
    )
    SELECT CASE max(rank) WHEN 3 THEN 'manager' WHEN 2 THEN 'editor' WHEN 1 THEN 'viewer' END
    FROM candidates
    HAVING max(rank) IS NOT NULL
$$;
-- +goose StatementEnd
DROP FUNCTION baleia_workspace_member_role(UUID, UUID);
DROP TABLE workspace_members;
