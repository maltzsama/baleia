-- +goose Up

-- UI language preference (#67). NULL means "follow the browser"; the locale
-- selection lives with the user so it follows them across devices.
ALTER TABLE users
    ADD COLUMN locale TEXT;

ALTER TABLE users ADD CONSTRAINT users_locale_check
    CHECK (locale IN ('en', 'pt-BR'));

-- +goose Down

ALTER TABLE users DROP CONSTRAINT IF EXISTS users_locale_check;

ALTER TABLE users DROP COLUMN IF EXISTS locale;
