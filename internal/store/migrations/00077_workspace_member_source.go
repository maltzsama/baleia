package migrations

import (
	"context"

	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(up00077, down00077)
}

const up00077Postgres = `
-- #653: a workspace role comes from a workspace admin ('manual') or from the
-- member's identity-provider groups ('idp_group'), refreshed at each sign-in.
-- A manual role is never overwritten by a group; a group role is removed when
-- the member leaves the group.
ALTER TABLE workspace_members
    ADD COLUMN source TEXT NOT NULL DEFAULT 'manual' CHECK (source IN ('manual', 'idp_group'));

-- Applies a sign-in's group roles (#653). Volatile PL/pgSQL so that, after
-- the per-user lock is taken, each statement reads what earlier sign-ins
-- committed (a single SQL statement keeps the snapshot it started with).
-- Only workspaces of teams the member is in; never a personal workspace;
-- never a role an admin set.
CREATE FUNCTION baleia_apply_group_workspace_roles(usr_id UUID, assignments JSONB)
RETURNS VOID LANGUAGE plpgsql VOLATILE AS $$
DECLARE
    keep UUID[];
BEGIN
    PERFORM pg_advisory_xact_lock(653, hashtext(usr_id::text));

    SELECT COALESCE(array_agg(w.id), '{}') INTO keep
    FROM jsonb_array_elements(assignments) AS e
    JOIN workspaces w ON w.id = (e ->> 'workspace_id')::uuid AND w.type = 'shared'
    JOIN team_members tm ON tm.team_id = w.owner_team_id AND tm.user_id = usr_id;

    INSERT INTO workspace_members (workspace_id, user_id, role, source)
    SELECT (e ->> 'workspace_id')::uuid, usr_id, e ->> 'role', 'idp_group'
    FROM jsonb_array_elements(assignments) AS e
    WHERE (e ->> 'workspace_id')::uuid = ANY (keep)
    ON CONFLICT (workspace_id, user_id) DO UPDATE
    SET role = EXCLUDED.role, granted_at = now()
    WHERE workspace_members.source = 'idp_group';

    DELETE FROM workspace_members d
    WHERE d.user_id = usr_id AND d.source = 'idp_group'
      AND NOT (d.workspace_id = ANY (keep));
END
$$;

`


const up00077MySQL = `
ALTER TABLE workspace_members
    ADD COLUMN source TEXT NOT NULL DEFAULT 'manual' CHECK (source IN ('manual', 'idp_group'));
`


const down00077Postgres = `
DROP FUNCTION baleia_apply_group_workspace_roles(UUID, JSONB);
ALTER TABLE workspace_members DROP COLUMN source;
`


const down00077MySQL = `
DROP FUNCTION baleia_apply_group_workspace_roles(CHAR(36), JSON);
ALTER TABLE workspace_members DROP COLUMN source;
`


func up00077(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, up00077Postgres, up00077MySQL)
}

func down00077(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, down00077Postgres, down00077MySQL)
}
