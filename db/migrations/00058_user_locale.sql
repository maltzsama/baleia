-- +goose Up

-- UI language preference (#67). NULL means "follow the browser".
ALTER TABLE users ADD COLUMN locale TEXT CHECK (locale IN ('en', 'pt-BR'));

-- +goose Down

ALTER TABLE users DROP COLUMN IF EXISTS locale;
