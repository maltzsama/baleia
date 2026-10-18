-- +goose Up

-- Suspend, never delete (#256). status is the only disable switch; the
-- suspended_* columns explain when and why.
ALTER TABLE users
    ADD COLUMN status              TEXT        NOT NULL DEFAULT 'active',
    ADD COLUMN suspended_at        TIMESTAMPTZ,
    ADD COLUMN suspended_reason    TEXT,
    ADD COLUMN directory_synced_at TIMESTAMPTZ;

ALTER TABLE users ADD CONSTRAINT users_status_check
    CHECK (status IN ('active', 'suspended'));

-- +goose Down

ALTER TABLE users DROP CONSTRAINT IF EXISTS users_status_check;

ALTER TABLE users
    DROP COLUMN IF EXISTS directory_synced_at,
    DROP COLUMN IF EXISTS suspended_reason,
    DROP COLUMN IF EXISTS suspended_at,
    DROP COLUMN IF EXISTS status;
