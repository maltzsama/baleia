-- +goose Up
-- #653: a workspace role comes from a workspace admin ('manual') or from the
-- member's identity-provider groups ('idp_group'), refreshed at each sign-in.
-- A manual role is never overwritten by a group; a group role is removed when
-- the member leaves the group.
ALTER TABLE workspace_members
    ADD COLUMN source TEXT NOT NULL DEFAULT 'manual' CHECK (source IN ('manual', 'idp_group'));

-- +goose Down
ALTER TABLE workspace_members DROP COLUMN source;
