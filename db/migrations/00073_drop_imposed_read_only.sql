-- +goose Up
-- #651: Baleia no longer imposes read-only work on a workspace's catalogs. A
-- catalog linked to a workspace stays reachable by that workspace's members
-- only; what they may read or write in it is decided by the engine and the
-- catalog manager, or by the connection's service account. The grant's
-- read/write permission and the never-enforced default_readonly flag are no
-- longer read or written by the application. credentials_read_only stays: it
-- is the administrator's own statement that the connection's account is
-- read-only at the source.
--
-- The two columns stay for this release: API pods of the previous release
-- still name them during a rolling upgrade, so dropping them now would fail
-- their queries. New code inserts grants without a permission, so the column
-- gets a default. Dropping both columns is #654, in the next release.

ALTER TABLE workspace_catalog_grants ALTER COLUMN permission SET DEFAULT 'write';

-- +goose Down
ALTER TABLE workspace_catalog_grants ALTER COLUMN permission DROP DEFAULT;
