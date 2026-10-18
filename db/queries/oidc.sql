-- name: CreateOIDCLoginAttempt :exec
INSERT INTO oidc_login_attempts (state, provider_id, nonce, code_verifier, redirect, expires_at)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: ConsumeOIDCLoginAttempt :one
-- Single use: the row is deleted atomically, so a replayed state finds nothing.
DELETE FROM oidc_login_attempts
WHERE state = $1
  AND expires_at > now()
RETURNING *;

-- name: DeleteExpiredOIDCLoginAttempts :exec
DELETE FROM oidc_login_attempts
WHERE expires_at <= now();
