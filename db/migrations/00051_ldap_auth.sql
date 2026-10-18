-- +goose Up

-- LDAP providers (#253). The type CHECK and the users.auth_provider CHECK are
-- inline constraints created by 00001, so PostgreSQL named them after the
-- column. chk_ldap_auth mirrors chk_oidc_auth: an LDAP user must be linked to
-- a provider and an external (directory) id.
ALTER TABLE auth_providers DROP CONSTRAINT auth_providers_type_check;
ALTER TABLE auth_providers ADD CONSTRAINT auth_providers_type_check
    CHECK (type IN ('oidc', 'ldap'));

ALTER TABLE users DROP CONSTRAINT users_auth_provider_check;
ALTER TABLE users ADD CONSTRAINT users_auth_provider_check
    CHECK (auth_provider IN ('local', 'oidc', 'ldap'));

ALTER TABLE users ADD CONSTRAINT chk_ldap_auth
    CHECK (auth_provider != 'ldap' OR (auth_provider_id IS NOT NULL AND external_id IS NOT NULL));

-- +goose Down

ALTER TABLE users DROP CONSTRAINT IF EXISTS chk_ldap_auth;

ALTER TABLE users DROP CONSTRAINT users_auth_provider_check;
ALTER TABLE users ADD CONSTRAINT users_auth_provider_check
    CHECK (auth_provider IN ('local', 'oidc'));

ALTER TABLE auth_providers DROP CONSTRAINT auth_providers_type_check;
ALTER TABLE auth_providers ADD CONSTRAINT auth_providers_type_check
    CHECK (type IN ('oidc'));
