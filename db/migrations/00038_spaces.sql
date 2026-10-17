-- +goose Up

-- Spaces (#40, ADR 0013): named containers inside a workspace with their own
-- access list, owning folders, saved queries and semantic metrics.
--
-- A workspace always has exactly one default space (is_default), created with
-- the workspace. Today's behaviour is preserved: every member may edit content
-- in the default space (default_role = 'editor'). Semantic docs stay
-- workspace-scoped: they describe tables, not content.

CREATE TABLE spaces (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id  UUID NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    name          TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 120),
    description   TEXT,
    visibility    TEXT NOT NULL CHECK (visibility IN ('workspace', 'restricted')),
    default_role  TEXT NOT NULL DEFAULT 'viewer' CHECK (default_role IN ('viewer', 'editor')),
    is_default    BOOLEAN NOT NULL DEFAULT false,
    created_by    UUID REFERENCES users (id) ON DELETE SET NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, name),
    UNIQUE (workspace_id, id),            -- target of the composite FKs below
    CHECK (NOT is_default OR visibility = 'workspace')
);
CREATE UNIQUE INDEX spaces_one_default ON spaces (workspace_id) WHERE is_default;

CREATE TABLE space_members (
    space_id        UUID NOT NULL REFERENCES spaces (id) ON DELETE CASCADE,
    principal_type  TEXT NOT NULL CHECK (principal_type IN ('user', 'team')),
    principal_id    UUID NOT NULL,       -- users.id or teams.id; existence is checked by the handler
    role            TEXT NOT NULL CHECK (role IN ('viewer', 'editor', 'manager')),
    granted_by      UUID REFERENCES users (id) ON DELETE SET NULL,
    granted_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (space_id, principal_type, principal_id)
);
CREATE INDEX space_members_principal ON space_members (principal_type, principal_id);

-- Backfill: one default space per existing workspace, editable by every member
-- (exactly today's behaviour).
INSERT INTO spaces (workspace_id, name, visibility, default_role, is_default)
SELECT id, 'General', 'workspace', 'editor', true FROM workspaces;

-- Clean up references that crossed workspaces (possible through the old API).
UPDATE query_folders c SET parent_id = NULL
  FROM query_folders p WHERE c.parent_id = p.id AND p.workspace_id <> c.workspace_id;
UPDATE saved_queries q SET folder_id = NULL
  FROM query_folders f WHERE q.folder_id = f.id AND f.workspace_id <> q.workspace_id;

-- Folders and saved queries move into spaces. The composite FKs make
-- cross-space (and therefore cross-workspace) references impossible.
ALTER TABLE query_folders ADD COLUMN space_id UUID;
UPDATE query_folders f SET space_id = s.id
  FROM spaces s WHERE s.workspace_id = f.workspace_id AND s.is_default;

ALTER TABLE saved_queries
    ADD COLUMN space_id UUID,
    ADD COLUMN updated_by UUID REFERENCES users (id) ON DELETE SET NULL;
UPDATE saved_queries q SET space_id = s.id
  FROM spaces s WHERE s.workspace_id = q.workspace_id AND s.is_default;

ALTER TABLE query_folders
    DROP CONSTRAINT query_folders_parent_id_fkey,
    ALTER COLUMN space_id SET NOT NULL,
    ADD CONSTRAINT query_folders_space_fk FOREIGN KEY (workspace_id, space_id)
        REFERENCES spaces (workspace_id, id) ON DELETE CASCADE,
    ADD CONSTRAINT query_folders_space_id_uq UNIQUE (space_id, id);
-- Separate statement: the FK below needs the unique constraint above to exist.
ALTER TABLE query_folders
    ADD CONSTRAINT query_folders_parent_same_space FOREIGN KEY (space_id, parent_id)
        REFERENCES query_folders (space_id, id) ON DELETE CASCADE
        DEFERRABLE INITIALLY IMMEDIATE;

ALTER TABLE saved_queries
    DROP CONSTRAINT saved_queries_folder_id_fkey,
    ALTER COLUMN space_id SET NOT NULL,
    ADD CONSTRAINT saved_queries_space_fk FOREIGN KEY (workspace_id, space_id)
        REFERENCES spaces (workspace_id, id) ON DELETE CASCADE,
    ADD CONSTRAINT saved_queries_folder_same_space FOREIGN KEY (space_id, folder_id)
        REFERENCES query_folders (space_id, id) ON DELETE SET NULL (folder_id)
        DEFERRABLE INITIALLY IMMEDIATE;
CREATE INDEX saved_queries_space ON saved_queries (space_id);
CREATE INDEX query_folders_space ON query_folders (space_id);

-- Saved queries remember where they run (#13): opening one restores its target.
ALTER TABLE saved_queries
    ADD COLUMN target_binding_id UUID REFERENCES catalog_bindings (id) ON DELETE SET NULL,
    ADD COLUMN target_schema TEXT;

-- Portable identity (ADR 0013, used by #34 bundles).
ALTER TABLE saved_queries ADD COLUMN slug TEXT;
ALTER TABLE query_folders ADD COLUMN slug TEXT;

-- +goose StatementBegin
DO $baleia$
DECLARE
    r         RECORD;
    base      TEXT;
    candidate TEXT;
    n         INT;
BEGIN
    FOR r IN SELECT id, workspace_id, name FROM saved_queries ORDER BY created_at, id LOOP
        base := btrim(regexp_replace(lower(r.name), '[^a-z0-9]+', '_', 'g'), '_');
        IF base = '' THEN base := 'q'; END IF;
        IF base !~ '^[a-z]' THEN base := 'q_' || base; END IF;
        base := left(base, 63);
        candidate := base;
        n := 1;
        WHILE EXISTS (SELECT 1 FROM saved_queries WHERE workspace_id = r.workspace_id AND slug = candidate AND id <> r.id) LOOP
            n := n + 1;
            candidate := left(base, 63 - length('_' || n::text)) || '_' || n::text;
        END LOOP;
        UPDATE saved_queries SET slug = candidate WHERE id = r.id;
    END LOOP;

    FOR r IN SELECT id, space_id, name FROM query_folders ORDER BY created_at, id LOOP
        base := btrim(regexp_replace(lower(r.name), '[^a-z0-9]+', '_', 'g'), '_');
        IF base = '' THEN base := 'q'; END IF;
        IF base !~ '^[a-z]' THEN base := 'q_' || base; END IF;
        base := left(base, 63);
        candidate := base;
        n := 1;
        WHILE EXISTS (SELECT 1 FROM query_folders WHERE space_id = r.space_id AND slug = candidate AND id <> r.id) LOOP
            n := n + 1;
            candidate := left(base, 63 - length('_' || n::text)) || '_' || n::text;
        END LOOP;
        UPDATE query_folders SET slug = candidate WHERE id = r.id;
    END LOOP;
END $baleia$;
-- +goose StatementEnd

ALTER TABLE saved_queries
    ALTER COLUMN slug SET NOT NULL,
    ADD CONSTRAINT saved_queries_slug_format CHECK (slug ~ '^[a-z][a-z0-9_]{0,62}$'),
    ADD CONSTRAINT saved_queries_slug_uq UNIQUE (workspace_id, slug);
ALTER TABLE query_folders
    ALTER COLUMN slug SET NOT NULL,
    ADD CONSTRAINT query_folders_slug_format CHECK (slug ~ '^[a-z][a-z0-9_]{0,62}$'),
    ADD CONSTRAINT query_folders_slug_uq UNIQUE (space_id, slug);

ALTER TABLE spaces ADD COLUMN slug TEXT;
-- +goose StatementBegin
DO $baleia$
DECLARE
    r         RECORD;
    base      TEXT;
    candidate TEXT;
    n         INT;
BEGIN
    FOR r IN SELECT id, workspace_id, name, is_default FROM spaces ORDER BY created_at, id LOOP
        IF r.is_default THEN
            base := 'general';
        ELSE
            base := btrim(regexp_replace(lower(r.name), '[^a-z0-9]+', '_', 'g'), '_');
            IF base = '' THEN base := 'space'; END IF;
            IF base !~ '^[a-z]' THEN base := 'space_' || base; END IF;
            base := left(base, 63);
        END IF;
        candidate := base;
        n := 1;
        WHILE EXISTS (SELECT 1 FROM spaces WHERE workspace_id = r.workspace_id AND slug = candidate AND id <> r.id) LOOP
            n := n + 1;
            candidate := left(base, 63 - length('_' || n::text)) || '_' || n::text;
        END LOOP;
        UPDATE spaces SET slug = candidate WHERE id = r.id;
    END LOOP;
END $baleia$;
-- +goose StatementEnd
ALTER TABLE spaces
    ALTER COLUMN slug SET NOT NULL,
    ADD CONSTRAINT spaces_slug_format CHECK (slug ~ '^[a-z][a-z0-9_]{0,62}$'),
    ADD CONSTRAINT spaces_slug_uq UNIQUE (workspace_id, slug);

-- Semantic metrics are content items too (#33): they belong to a space.
ALTER TABLE semantic_metrics ADD COLUMN space_id UUID;
UPDATE semantic_metrics m SET space_id = s.id
  FROM spaces s WHERE s.workspace_id = m.workspace_id AND s.is_default;
ALTER TABLE semantic_metrics
    ALTER COLUMN space_id SET NOT NULL,
    ADD CONSTRAINT semantic_metrics_space_fk FOREIGN KEY (workspace_id, space_id)
        REFERENCES spaces (workspace_id, id) ON DELETE CASCADE;
CREATE INDEX semantic_metrics_space ON semantic_metrics (space_id);

-- Effective space role: the highest of (global admin / workspace owner) =
-- manager, the space's default_role for a workspace member when visibility is
-- 'workspace', and explicit user/team entries. A caller who is not a member of
-- the workspace has no role, even with a stale entry. NULL means "invisible".
-- +goose StatementBegin
CREATE FUNCTION baleia_effective_space_role(ws_id UUID, sp_id UUID, usr_id UUID)
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

-- Every new workspace gets its default space in the same transaction, whatever
-- the creation path (API, personal workspace on first login, future ones).
-- +goose StatementBegin
CREATE FUNCTION workspaces_create_default_space() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO spaces (workspace_id, name, visibility, default_role, is_default, slug)
    VALUES (NEW.id, 'General', 'workspace', 'editor', true, 'general');
    RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER workspaces_default_space AFTER INSERT ON workspaces
    FOR EACH ROW EXECUTE FUNCTION workspaces_create_default_space();

-- +goose Down

DROP TRIGGER IF EXISTS workspaces_default_space ON workspaces;
DROP FUNCTION IF EXISTS workspaces_create_default_space();
DROP FUNCTION IF EXISTS baleia_effective_space_role(UUID, UUID, UUID);

ALTER TABLE semantic_metrics DROP CONSTRAINT IF EXISTS semantic_metrics_space_fk;
ALTER TABLE semantic_metrics DROP COLUMN IF EXISTS space_id;

ALTER TABLE saved_queries
    DROP CONSTRAINT IF EXISTS saved_queries_folder_same_space,
    DROP CONSTRAINT IF EXISTS saved_queries_space_fk,
    DROP CONSTRAINT IF EXISTS saved_queries_slug_uq,
    DROP CONSTRAINT IF EXISTS saved_queries_slug_format,
    DROP COLUMN IF EXISTS slug,
    DROP COLUMN IF EXISTS target_binding_id,
    DROP COLUMN IF EXISTS target_schema,
    DROP COLUMN IF EXISTS updated_by,
    DROP COLUMN IF EXISTS space_id;
ALTER TABLE saved_queries
    ADD CONSTRAINT saved_queries_folder_id_fkey FOREIGN KEY (folder_id)
        REFERENCES query_folders (id) ON DELETE SET NULL;

ALTER TABLE query_folders
    DROP CONSTRAINT IF EXISTS query_folders_parent_same_space,
    DROP CONSTRAINT IF EXISTS query_folders_space_fk,
    DROP CONSTRAINT IF EXISTS query_folders_space_id_uq,
    DROP CONSTRAINT IF EXISTS query_folders_slug_uq,
    DROP CONSTRAINT IF EXISTS query_folders_slug_format,
    DROP COLUMN IF EXISTS slug,
    DROP COLUMN IF EXISTS space_id;
ALTER TABLE query_folders
    ADD CONSTRAINT query_folders_parent_id_fkey FOREIGN KEY (parent_id)
        REFERENCES query_folders (id) ON DELETE CASCADE;

DROP TABLE IF EXISTS space_members;
DROP TABLE IF EXISTS spaces;
