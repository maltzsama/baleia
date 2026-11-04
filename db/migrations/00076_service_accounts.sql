-- +goose Up
-- #653: Baleia service accounts, the non-human principals agents and other
-- clients sign in as. A service account is a user of kind 'service_account':
-- it has workspace roles like a person, never the global admin role, and no
-- password. It signs in with a Baleia token (an API key) or, when linked to an
-- identity provider (auth_provider 'oidc', external_id 'client:<client id>'),
-- with that provider's client-credentials access token, which is then also
-- its identity towards per-user catalogs (ADR 0015). home_workspace_id is the
-- workspace it was created in, whose admins manage it.
ALTER TABLE users
    ADD COLUMN kind TEXT NOT NULL DEFAULT 'human' CHECK (kind IN ('human', 'service_account')),
    ADD COLUMN home_workspace_id UUID REFERENCES workspaces (id) ON DELETE SET NULL,
    ADD COLUMN description TEXT;

ALTER TABLE users DROP CONSTRAINT users_auth_provider_check;
ALTER TABLE users ADD CONSTRAINT users_auth_provider_check
    CHECK (auth_provider IN ('local', 'oidc', 'ldap', 'service'));
ALTER TABLE users ADD CONSTRAINT chk_service_auth
    CHECK (auth_provider <> 'service' OR kind = 'service_account');
ALTER TABLE users ADD CONSTRAINT chk_service_account
    CHECK (kind <> 'service_account' OR (role = 'member' AND password_hash IS NULL AND auth_provider IN ('service', 'oidc')));

-- A service account belongs to a shared workspace through its row here, not
-- through the team; everything else is as in 00075.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION baleia_workspace_member_role(ws_id UUID, usr_id UUID)
RETURNS TEXT LANGUAGE sql STABLE AS $$
    SELECT CASE
        WHEN w.type = 'personal' AND w.owner_user_id = usr_id THEN 'admin'
        WHEN w.type = 'personal' THEN wm.role
        WHEN u.kind = 'service_account' THEN wm.role
        WHEN tm.user_id IS NULL THEN NULL
        ELSE COALESCE(wm.role, CASE tm.role WHEN 'owner' THEN 'admin' ELSE 'editor' END)
    END
    FROM workspaces w
    LEFT JOIN users u ON u.id = usr_id
    LEFT JOIN team_members tm ON tm.team_id = w.owner_team_id AND tm.user_id = usr_id
    LEFT JOIN workspace_members wm ON wm.workspace_id = w.id AND wm.user_id = usr_id
    WHERE w.id = ws_id
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION baleia_workspace_member_role(ws_id UUID, usr_id UUID)
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
DELETE FROM users WHERE kind = 'service_account';
ALTER TABLE users DROP CONSTRAINT chk_service_account;
ALTER TABLE users DROP CONSTRAINT chk_service_auth;
ALTER TABLE users DROP CONSTRAINT users_auth_provider_check;
ALTER TABLE users ADD CONSTRAINT users_auth_provider_check
    CHECK (auth_provider IN ('local', 'oidc', 'ldap'));
ALTER TABLE users DROP COLUMN description, DROP COLUMN home_workspace_id, DROP COLUMN kind;
