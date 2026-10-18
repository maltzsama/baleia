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

-- name: CountUsersByAuthProvider :one
-- Delete of a provider (#285) refuses while users are still linked.
SELECT count(*) FROM users WHERE auth_provider_id = $1;

-- name: RevokeRefreshTokensByAuthProvider :exec
UPDATE refresh_tokens
SET revoked = true
WHERE revoked = false
  AND user_id IN (SELECT id FROM users WHERE auth_provider_id = $1);

-- name: DeleteAPIKeysByAuthProvider :exec
DELETE FROM api_keys
WHERE user_id IN (SELECT id FROM users WHERE auth_provider_id = $1);

-- name: SuspendUsersByAuthProvider :exec
-- Deleting a provider (#285) suspends and detaches its users first: the FK's
-- ON DELETE SET NULL would otherwise violate chk_oidc_auth/chk_ldap_auth. The
-- sentinel hash can never match a password, so a detached account cannot log in.
UPDATE users
SET status = 'suspended',
    suspended_at = now(),
    suspended_reason = 'identity_provider_deleted',
    auth_provider = 'local',
    auth_provider_id = NULL,
    external_id = NULL,
    password_hash = COALESCE(password_hash, '!'),
    updated_at = now()
WHERE auth_provider_id = $1;

-- name: DeleteAuthProvider :exec
DELETE FROM auth_providers WHERE id = $1;