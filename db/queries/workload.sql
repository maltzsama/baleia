-- Ordered, admin-managed workload rules (#399, ADR 0014, #539).

-- name: ListWorkloadRules :many
SELECT * FROM workload_rules
ORDER BY position, created_at;

-- name: GetWorkloadRule :one
SELECT * FROM workload_rules WHERE id = $1;

-- name: MaxWorkloadRulePosition :one
SELECT COALESCE(MAX(position), 0)::integer FROM workload_rules;

-- name: CreateWorkloadRule :one
INSERT INTO workload_rules (position, enabled, match, class, max_class, weight, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: UpdateWorkloadRule :one
UPDATE workload_rules
SET position = $2,
    enabled = $3,
    match = $4,
    class = $5,
    max_class = $6,
    weight = $7,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: DeleteWorkloadRule :execrows
DELETE FROM workload_rules WHERE id = $1;
