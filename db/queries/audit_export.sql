-- name: ListAuditEventsForExport :many
-- The batch after the cursor, before the safety-lag cutoff.
SELECT occurred_at, id, actor_kind,
       coalesce(actor_id, '')::text AS actor_id,
       coalesce(auth_method, '')::text AS auth_method,
       action, resource_type,
       coalesce(resource_id, '')::text AS resource_id,
       coalesce(workspace_id::text, '')::text AS workspace_id,
       outcome,
       coalesce(reason, '')::text AS reason,
       coalesce(request_id, '')::text AS request_id,
       coalesce(trace_id, '')::text AS trace_id,
       coalesce(host(client_ip), '')::text AS client_ip,
       coalesce(user_agent, '')::text AS user_agent,
       changes
FROM audit_events
WHERE (occurred_at, id) > (sqlc.arg(after_at)::timestamptz, sqlc.arg(after_id)::bigint)
  AND occurred_at < sqlc.arg(before)::timestamptz
ORDER BY occurred_at, id
LIMIT sqlc.arg(lim);

-- name: GetAuditExportCursor :one
SELECT occurred_at, id FROM audit_export_cursor WHERE sink = $1;

-- name: SetAuditExportCursor :exec
INSERT INTO audit_export_cursor (sink, occurred_at, id, updated_at)
VALUES ($1, $2, $3, now())
ON CONFLICT (sink) DO UPDATE SET occurred_at = EXCLUDED.occurred_at, id = EXCLUDED.id, updated_at = now();
