package migrations

import (
	"context"

	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(up00079, down00079)
}

const up00079Postgres = `
-- #702 / ADR 0018: the workspace catalog scope. One SQL function becomes the
-- single source of truth for "which catalogs a workspace may use"; a
-- pending-request table carries the two-party grant flow; and the #654 columns
-- go away with the personal auto-reach rule.

-- The scope: the only reader of workspace_catalog_grants outside grant CRUD.
CREATE FUNCTION baleia_workspace_catalog_ids(ws uuid) RETURNS SETOF uuid
LANGUAGE sql STABLE AS $$
    SELECT g.catalog_id
    FROM workspace_catalog_grants g
    JOIN catalogs c ON c.id = g.catalog_id
    WHERE g.workspace_id = ws AND c.enabled
$$;

-- Pending grant requests, kept apart from the active grants table.
CREATE TABLE catalog_grant_requests (
    workspace_id      UUID        NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    catalog_id        UUID        NOT NULL REFERENCES catalogs(id)   ON DELETE CASCADE,
    initiated_by_side TEXT        NOT NULL CHECK (initiated_by_side IN ('catalog', 'workspace')),
    requested_by      UUID        REFERENCES users(id) ON DELETE SET NULL,
    requested_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, catalog_id)
);

-- Discoverability for the workspace-admin request picker.
ALTER TABLE catalogs ADD COLUMN discoverable BOOLEAN NOT NULL DEFAULT false;

-- #654 ('permission', 'default_readonly') stays for now: a 0.6.1 pod still
-- names both during the rolling update ('RETURNING *' on a grant read,
-- 'SELECT *' on every connection read), so dropping them here would break
-- those pods. They are dropped in a later release, once 0.6.1 is gone.

-- Backfill: an explicit grant for every (personal workspace, catalog of the
-- owner's connections) pair the personal auto-reach rule reached, so nobody
-- loses access when the rule is dropped. Not reversed in Down: a backfilled
-- grant is indistinguishable from a real one.
INSERT INTO workspace_catalog_grants (workspace_id, catalog_id, granted_by)
SELECT w.id, c.id, w.owner_user_id
FROM workspaces w
JOIN catalog_connections cc ON cc.created_by = w.owner_user_id
JOIN catalogs c ON c.connection_id = cc.id
WHERE w.type = 'personal' AND w.owner_user_id IS NOT NULL
ON CONFLICT (workspace_id, catalog_id) DO NOTHING;
`


const up00079MySQL = `
CREATE TABLE catalog_grant_requests (
    workspace_id      CHAR(36)        NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    catalog_id        CHAR(36)        NOT NULL REFERENCES catalogs(id)   ON DELETE CASCADE,
    initiated_by_side TEXT        NOT NULL CHECK (initiated_by_side IN ('catalog', 'workspace')),
    requested_by      CHAR(36)        REFERENCES users(id) ON DELETE SET NULL,
    requested_at      DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (workspace_id, catalog_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

ALTER TABLE catalogs ADD COLUMN discoverable BOOLEAN NOT NULL DEFAULT false;
`


const down00079Postgres = `
DROP FUNCTION IF EXISTS baleia_workspace_catalog_ids(uuid);
DROP TABLE IF EXISTS catalog_grant_requests;
ALTER TABLE catalogs DROP COLUMN IF EXISTS discoverable;
`


const down00079MySQL = `
DROP FUNCTION IF EXISTS baleia_workspace_catalog_ids(CHAR(36));
DROP TABLE IF EXISTS catalog_grant_requests;
ALTER TABLE catalogs DROP COLUMN discoverable;
`


func up00079(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, up00079Postgres, up00079MySQL)
}

func down00079(ctx context.Context, db *bun.DB) error {
	return dialectDDL(ctx, db, down00079Postgres, down00079MySQL)
}
