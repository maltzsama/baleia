-- UI language preference (#67).

-- name: SetUserLocale :exec
UPDATE users SET locale = $2, updated_at = now() WHERE id = $1;
