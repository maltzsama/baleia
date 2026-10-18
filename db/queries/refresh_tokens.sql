-- name: CreateRefreshToken :one
INSERT INTO refresh_tokens (user_id, token_hash, expires_at, family_id)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: ConsumeRefreshToken :one
-- Atomic rotation: of two concurrent refreshes with the same token, exactly one
-- updates a row (#51).
UPDATE refresh_tokens
SET revoked = true
WHERE token_hash = $1
  AND revoked = false
  AND expires_at > now()
RETURNING id, user_id, family_id;

-- name: GetRefreshTokenByHash :one
SELECT * FROM refresh_tokens
WHERE token_hash = $1
  AND revoked = false
  AND expires_at > now();

-- name: GetRefreshTokenByHashAny :one
-- Reuse detection looks up the row ConsumeRefreshToken refused, including a
-- revoked one (#51).
SELECT * FROM refresh_tokens
WHERE token_hash = $1;

-- name: RevokeRefreshToken :exec
UPDATE refresh_tokens
SET revoked = true
WHERE id = $1;

-- name: RevokeRefreshFamily :exec
UPDATE refresh_tokens
SET revoked = true
WHERE family_id = $1
  AND revoked = false;

-- name: RevokeUserRefreshTokens :exec
UPDATE refresh_tokens
SET revoked = true
WHERE user_id = $1
  AND revoked = false;

-- name: DeleteExpiredRefreshTokens :exec
DELETE FROM refresh_tokens
WHERE expires_at < now() OR revoked = true;
