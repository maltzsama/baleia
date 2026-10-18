-- name: GetAuthProviderByID :one
SELECT * FROM auth_providers WHERE id = $1;

-- name: ListAuthProviders :many
SELECT * FROM auth_providers WHERE enabled = true ORDER BY name;

-- name: ListAllAuthProviders :many
-- Admin listing (#259): includes disabled providers so they can be re-enabled.
SELECT * FROM auth_providers ORDER BY name;

-- name: CreateAuthProvider :one
INSERT INTO auth_providers (name, type, config_enc, enabled)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: UpdateAuthProvider :one
UPDATE auth_providers
SET name = $2, config_enc = $3, enabled = $4, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: GetUserByID :one
SELECT * FROM users WHERE id = $1;

-- name: GetUserByEmail :one
SELECT * FROM users WHERE email = $1;

-- name: GetUserByExternalID :one
SELECT * FROM users
WHERE auth_provider_id = $1 AND external_id = $2;

-- name: CreateUser :one
INSERT INTO users (name, email, password_hash, role, auth_provider, auth_provider_id, external_id)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: UpdateUser :one
UPDATE users
SET name = $2, email = $3, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: UpdateUserPersonalWorkspace :exec
UPDATE users
SET personal_workspace_id = $2, updated_at = now()
WHERE id = $1;

-- name: ListUsers :many
SELECT * FROM users ORDER BY name;

-- name: CreateAPIKey :one
INSERT INTO api_keys (user_id, name, key_hash, prefix, scopes, expires_at)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetAPIKeyByHash :one
SELECT * FROM api_keys WHERE key_hash = $1;

-- name: TouchAPIKey :exec
UPDATE api_keys SET last_used_at = now() WHERE id = $1;

-- name: ListAPIKeysByUser :many
SELECT * FROM api_keys WHERE user_id = $1 ORDER BY created_at DESC;

-- name: DeleteAPIKey :exec
DELETE FROM api_keys WHERE id = $1 AND user_id = $2;


-- name: UpdateUserPassword :exec
UPDATE users
SET password_hash = $2,
    updated_at = now()
WHERE id = $1;

-- name: UpdateUserRole :exec
UPDATE users SET role = $2, updated_at = now() WHERE id = $1;

-- name: DeleteUser :exec
DELETE FROM users WHERE id = $1;

-- name: GetAPIKeyByID :one
SELECT * FROM api_keys WHERE id = $1 AND user_id = $2;

-- name: DeleteAPIKeysByUser :exec
-- Suspension revokes every API key of the user (#258).
DELETE FROM api_keys WHERE user_id = $1;

-- name: ListEnabledLDAPProviders :many
SELECT * FROM auth_providers WHERE enabled = true AND type = 'ldap' ORDER BY name;

-- name: ListUsersByAuthProvider :many
SELECT * FROM users WHERE auth_provider_id = $1 ORDER BY created_at;

-- name: SuspendUser :one
-- Suspension is the only disable switch (#54). The reason is bounded:
-- 'admin', 'policy' or 'directory'.
UPDATE users
SET status = 'suspended',
    suspended_at = now(),
    suspended_reason = $2,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: ReinstateUser :one
UPDATE users
SET status = 'active',
    suspended_at = NULL,
    suspended_reason = NULL,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: ReinstateDirectoryUser :exec
-- Reconciliation only lifts its own suspensions (#257): an admin suspension
-- survives a directory pass.
UPDATE users
SET status = 'active',
    suspended_at = NULL,
    suspended_reason = NULL,
    updated_at = now()
WHERE id = $1
  AND status = 'suspended'
  AND suspended_reason = 'directory';

-- name: MarkDirectorySynced :exec
UPDATE users SET directory_synced_at = now(), updated_at = now() WHERE id = $1;