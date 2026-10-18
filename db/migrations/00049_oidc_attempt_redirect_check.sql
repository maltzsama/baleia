-- +goose Up

-- Defence in depth for the stored post-login redirect (#252): the handler
-- validates it too, but the database refuses scheme-relative, backslash and
-- control-character escapes even if a future writer forgets.
ALTER TABLE oidc_login_attempts
    ADD CONSTRAINT oidc_login_attempts_redirect_check
    CHECK (
        redirect IS NULL
        OR (
            redirect LIKE '/%'
            AND redirect NOT LIKE '//%'
            AND redirect !~ '\\'
            AND redirect !~ '[\r\n\t]'
        )
    );

-- +goose Down

ALTER TABLE oidc_login_attempts
    DROP CONSTRAINT IF EXISTS oidc_login_attempts_redirect_check;
