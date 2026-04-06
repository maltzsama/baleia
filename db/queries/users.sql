-- name: GetAuthProviderByID :one
SELECT * FROM auth_providers WHERE id = $1;

-- name: ListAuthProviders :many
SELECT * FROM auth_providers WHERE enabled = true ORDER BY name;

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
INSERT INTO api_keys (user_id, name, key_hash, expires_at)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetAPIKeyByHash :one
SELECT * FROM api_keys WHERE key_hash = $1;

-- name: ListAPIKeysByUser :many
SELECT * FROM api_keys WHERE user_id = $1 ORDER BY created_at DESC;

-- name: DeleteAPIKey :exec
DELETE FROM api_keys WHERE id = $1 AND user_id = $2;


-- name: SetPasswordResetToken :exec
UPDATE users
SET password_reset_token = $2, password_reset_expires_at = $3
WHERE email = $1;

-- name: GetUserByResetToken :one
SELECT * FROM users
WHERE password_reset_token = $1
  AND password_reset_expires_at > now();

-- name: UpdateUserPassword :exec
UPDATE users
SET password_hash = $2,
    password_reset_token = NULL,
    password_reset_expires_at = NULL,
    updated_at = now()
WHERE id = $1;