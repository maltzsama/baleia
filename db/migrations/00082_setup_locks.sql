-- +goose Up
-- ADR 0020 forbids advisory locks: a stable row serializes the first-admin
-- setup instead of a session lock.
CREATE TABLE setup_locks (
    name TEXT PRIMARY KEY NOT NULL
);

-- +goose Down
DROP TABLE setup_locks;
