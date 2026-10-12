-- +goose Up

CREATE TABLE teams (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT        NOT NULL,
    description TEXT,
    created_by  UUID          NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    created_at  TIMESTAMPTZ   NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ   NOT NULL DEFAULT now()
);

CREATE TABLE team_members (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    team_id     UUID          NOT NULL REFERENCES teams (id) ON DELETE CASCADE,
    user_id     UUID          NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    role        TEXT        NOT NULL DEFAULT 'member' CHECK (role IN ('owner', 'member')),
    joined_at   TIMESTAMPTZ   NOT NULL DEFAULT now(),
    UNIQUE (team_id, user_id)
);

CREATE TABLE workspaces (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name                 TEXT        NOT NULL,
    description          TEXT,
    type                 TEXT        NOT NULL CHECK (type IN ('personal', 'shared')),
    polaris_catalog_name TEXT        NOT NULL UNIQUE,
    owner_user_id        UUID          REFERENCES users (id) ON DELETE RESTRICT,
    owner_team_id        UUID          REFERENCES teams (id) ON DELETE RESTRICT,
    created_at           TIMESTAMPTZ   NOT NULL DEFAULT now(),
    updated_at           TIMESTAMPTZ   NOT NULL DEFAULT now(),
    CONSTRAINT chk_workspace_owner CHECK (
        (type = 'personal' AND owner_user_id IS NOT NULL AND owner_team_id IS NULL) OR
        (type = 'shared'   AND owner_team_id IS NOT NULL AND owner_user_id IS NULL)
    )
);

ALTER TABLE users
    ADD CONSTRAINT fk_personal_workspace
    FOREIGN KEY (personal_workspace_id)
    REFERENCES workspaces (id)
    ON DELETE SET NULL;

CREATE INDEX idx_teams_created_by       ON teams (created_by);
CREATE INDEX idx_team_members_team_id   ON team_members (team_id);
CREATE INDEX idx_team_members_user_id   ON team_members (user_id);
CREATE INDEX idx_workspaces_owner_user  ON workspaces (owner_user_id);
CREATE INDEX idx_workspaces_owner_team  ON workspaces (owner_team_id);

-- +goose Down

ALTER TABLE users DROP CONSTRAINT IF EXISTS fk_personal_workspace;
DROP TABLE IF EXISTS workspaces;
DROP TABLE IF EXISTS team_members;
DROP TABLE IF EXISTS teams;
