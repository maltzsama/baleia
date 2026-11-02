-- Trino resource groups (#599, A-5.2): the write path Baleia uses to publish
-- the generated document (#598) into the tables Trino's db manager reads, plus
-- the revision log that makes a rollback possible.

-- name: DeleteTrinoResourceGroups :exec
DELETE FROM resource_groups WHERE environment = $1;

-- name: InsertTrinoResourceGroup :one
INSERT INTO resource_groups (
    name, soft_memory_limit, max_queued, soft_concurrency_limit,
    hard_concurrency_limit, scheduling_policy, scheduling_weight, jmx_export,
    soft_cpu_limit, hard_cpu_limit, hard_physical_data_scan_limit, parent, environment
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
RETURNING resource_group_id;

-- name: InsertTrinoSelector :exec
INSERT INTO selectors (resource_group_id, priority, user_regex, original_user_regex,
    authenticated_user_regex, user_group_regex, source_regex, query_type, client_tags)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9);

-- name: NextTrinoResourceGroupRevision :one
SELECT COALESCE(MAX(revision), 0) + 1::integer FROM trino_resource_group_revisions
WHERE engine_id = $1;

-- name: InsertTrinoResourceGroupRevision :exec
INSERT INTO trino_resource_group_revisions (engine_id, revision, config)
VALUES ($1, $2, $3);

-- name: GetTrinoResourceGroupRevision :one
SELECT config FROM trino_resource_group_revisions
WHERE engine_id = $1 AND revision = $2;
