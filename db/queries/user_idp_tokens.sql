-- name: SaveUserIDPTokenOnSignIn :execrows
-- Keeps a sign-in's tokens only while the account is active and its provider
-- enabled. The share locks make a concurrent suspension or provider disable,
-- which deletes the tokens in the same transaction as its update, win: this
-- insert waits for it and then finds nothing to insert.
WITH allowed AS (
    SELECT u.id
    FROM users u
    JOIN auth_providers ap ON ap.id = @auth_provider_id::uuid
    WHERE u.id = @user_id::uuid AND u.status = 'active' AND ap.enabled
    FOR SHARE OF u, ap
)
INSERT INTO user_idp_tokens (user_id, auth_provider_id, access_token_enc, refresh_token_enc, expires_at)
SELECT @user_id::uuid, @auth_provider_id::uuid, @access_token_enc::text, sqlc.narg(refresh_token_enc)::text, @expires_at::timestamptz
FROM allowed
ON CONFLICT (user_id) DO UPDATE
SET auth_provider_id  = EXCLUDED.auth_provider_id,
    access_token_enc  = EXCLUDED.access_token_enc,
    refresh_token_enc = EXCLUDED.refresh_token_enc,
    expires_at        = EXCLUDED.expires_at,
    updated_at        = now();

-- name: GetUserIDPToken :one
SELECT * FROM user_idp_tokens WHERE user_id = $1;

-- name: GetUserIDPTokenForUpdate :one
-- Serializes a refresh across processes: one refreshes, the others wait and
-- read its result.
SELECT * FROM user_idp_tokens WHERE user_id = $1 FOR UPDATE;

-- name: UpdateUserIDPToken :exec
-- A refresh writes over the row it locked; it never recreates a deleted one.
UPDATE user_idp_tokens
SET access_token_enc  = @access_token_enc::text,
    refresh_token_enc = sqlc.narg(refresh_token_enc)::text,
    expires_at        = @expires_at::timestamptz,
    updated_at        = now()
WHERE user_id = @user_id::uuid;

-- name: DeleteUserIDPToken :exec
DELETE FROM user_idp_tokens WHERE user_id = $1;

-- name: DeleteUserIDPTokensByAuthProvider :exec
DELETE FROM user_idp_tokens WHERE auth_provider_id = $1;
