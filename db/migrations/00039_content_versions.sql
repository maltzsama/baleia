-- +goose Up

-- Content versions (#34, ADR 0013): an append-only history of every saved query
-- and semantic metric, written in the same transaction as the change. A
-- version says what the content was; the audit trail says who did what, from
-- where. They are complementary, so versions are not derived from audit.

CREATE TABLE content_versions (
    id            BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    workspace_id  UUID NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    -- Space at the time of the version. No FK: a deleted space (and its
    -- content) must still be restorable from the version history.
    space_id      UUID NOT NULL,
    entity_type   TEXT NOT NULL CHECK (entity_type IN ('saved_query', 'semantic_metric')),
    -- No FK: versions outlive deleted entities so trash/restore can bring them
    -- back with their original id.
    entity_id     UUID NOT NULL,
    version       INTEGER NOT NULL CHECK (version >= 1),
    change_kind   TEXT NOT NULL CHECK (change_kind IN ('create', 'update', 'delete', 'restore', 'import')),
    -- Full state after the change (before, for a delete).
    snapshot      JSONB NOT NULL,
    author_id     UUID REFERENCES users (id) ON DELETE SET NULL,
    restored_from INTEGER,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (entity_type, entity_id, version)
);
CREATE INDEX content_versions_ws_time ON content_versions (workspace_id, created_at DESC);
CREATE INDEX content_versions_entity ON content_versions (entity_type, entity_id, version DESC);
CREATE INDEX content_versions_deleted ON content_versions (workspace_id, entity_type, created_at DESC)
    WHERE change_kind = 'delete';

-- Append-only, same mechanism as audit_events (#45). The one exception is the
-- cascade from a workspace deletion: the parent row is already gone when the
-- cascaded delete fires.
-- +goose StatementBegin
CREATE FUNCTION content_versions_append_only() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' AND NOT EXISTS (SELECT 1 FROM workspaces WHERE id = OLD.workspace_id) THEN
        RETURN OLD;
    END IF;
    RAISE EXCEPTION 'content_versions is append-only (% blocked)', TG_OP;
END $$;
-- +goose StatementEnd
CREATE TRIGGER content_versions_no_update BEFORE UPDATE OR DELETE ON content_versions
    FOR EACH ROW EXECUTE FUNCTION content_versions_append_only();

-- Baseline: version 1 for everything that exists today.
INSERT INTO content_versions (workspace_id, space_id, entity_type, entity_id, version, change_kind, snapshot, author_id, created_at)
SELECT workspace_id, space_id, 'saved_query', id, 1, 'create',
       jsonb_build_object(
           'schema', 'saved_query/v1',
           'slug', slug, 'name', name, 'sql', sql, 'folder_id', folder_id,
           'space_id', space_id, 'target_binding_id', target_binding_id, 'target_schema', target_schema
       ),
       created_by, updated_at
FROM saved_queries;

INSERT INTO content_versions (workspace_id, space_id, entity_type, entity_id, version, change_kind, snapshot, author_id, created_at)
SELECT workspace_id, space_id, 'semantic_metric', id, 1, 'create',
       to_jsonb(m) - 'id' - 'workspace_id' - 'updated_at' - 'updated_by'
           || jsonb_build_object('schema', 'semantic_metric/v1'),
       updated_by, updated_at
FROM semantic_metrics m;

-- +goose Down

DROP TABLE IF EXISTS content_versions;
DROP FUNCTION IF EXISTS content_versions_append_only();
