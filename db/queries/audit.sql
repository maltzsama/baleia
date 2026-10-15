-- name: InsertAuditEvent :exec
INSERT INTO audit_events (actor_kind, actor_id, auth_method, action, resource_type, resource_id, workspace_id,
                          outcome, reason, request_id, trace_id, client_ip, user_agent, changes)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14);

-- name: ListAuditEvents :many
-- Newest first, keyset-paginated on (occurred_at, id): the cursor is the
-- last row of the previous page. Every filter is optional.
SELECT id, occurred_at, actor_kind, actor_id, auth_method, action, resource_type, resource_id, workspace_id,
       outcome, reason, request_id, trace_id, client_ip, user_agent, changes
FROM audit_events
WHERE (sqlc.narg(resource_type)::text IS NULL OR resource_type = sqlc.narg(resource_type)::text)
  AND (sqlc.narg(resource_id)::text IS NULL OR resource_id = sqlc.narg(resource_id)::text)
  AND (sqlc.narg(actor_id)::text IS NULL OR actor_id = sqlc.narg(actor_id)::text)
  AND (sqlc.narg(action)::text IS NULL OR action = sqlc.narg(action)::text)
  AND (sqlc.narg(outcome)::text IS NULL OR outcome = sqlc.narg(outcome)::text)
  AND (sqlc.narg(from_time)::timestamptz IS NULL OR occurred_at >= sqlc.narg(from_time)::timestamptz)
  AND (sqlc.narg(to_time)::timestamptz IS NULL OR occurred_at < sqlc.narg(to_time)::timestamptz)
  AND (sqlc.narg(cursor_at)::timestamptz IS NULL
       OR (occurred_at, id) < (sqlc.narg(cursor_at)::timestamptz, sqlc.narg(cursor_id)::bigint))
ORDER BY occurred_at DESC, id DESC
LIMIT @lim;

-- name: EnsureAuditPartitions :exec
-- Creates the monthly partitions of this month and the next months ahead.
SELECT audit_ensure_partitions(@months_ahead::int);
