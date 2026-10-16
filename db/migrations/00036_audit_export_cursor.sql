-- +goose Up

-- Export cursor for the audit trail (#50): the position (occurred_at, id) of
-- the last event a sink has accepted. One row per sink ('opensearch'). The
-- cursor only ever moves forward, and never past a failed item, so the export
-- is at-least-once, ordered and resumable. audit_events is never modified.
CREATE TABLE audit_export_cursor (
    sink        TEXT PRIMARY KEY,
    occurred_at TIMESTAMPTZ NOT NULL,
    id          BIGINT      NOT NULL,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down

DROP TABLE IF EXISTS audit_export_cursor;
