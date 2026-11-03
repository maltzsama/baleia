-- name: UpsertUserIDPToken :exec
INSERT INTO user_idp_tokens (user_id, auth_provider_id, access_token_enc, refresh_token_enc, expires_at)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (user_id) DO UPDATE
SET auth_provider_id  = EXCLUDED.auth_provider_id,
    access_token_enc  = EXCLUDED.access_token_enc,
    refresh_token_enc = EXCLUDED.refresh_token_enc,
    expires_at        = EXCLUDED.expires_at,
    updated_at        = now();

-- name: GetUserIDPToken :one
SELECT * FROM user_idp_tokens WHERE user_id = $1;

-- name: DeleteUserIDPToken :exec
DELETE FROM user_idp_tokens WHERE user_id = $1;

-- name: DeleteUserIDPTokensByAuthProvider :exec
DELETE FROM user_idp_tokens WHERE auth_provider_id = $1;
