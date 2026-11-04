-- name: CreateServiceAccount :one
-- A service account (#653): a user of kind 'service_account' with no password.
-- auth_provider is 'oidc' with external_id 'client:<client id>' when it is
-- linked to an identity provider, 'service' otherwise.
INSERT INTO users (name, email, role, auth_provider, auth_provider_id, external_id, kind, home_workspace_id, description)
VALUES (@name::text, @email::text, 'member', @auth_provider::text, sqlc.narg(auth_provider_id)::uuid,
        sqlc.narg(external_id)::text, 'service_account', sqlc.narg(home_workspace_id)::uuid, sqlc.narg(description)::text)
RETURNING *;

-- name: GetServiceAccount :one
SELECT * FROM users WHERE id = @id::uuid AND kind = 'service_account';

-- name: ListServiceAccounts :many
-- Every service account, for a global admin.
SELECT * FROM users WHERE kind = 'service_account' ORDER BY name;

-- name: ListServiceAccountsOfWorkspaces :many
-- The service accounts created in, or members of, the given workspaces.
SELECT DISTINCT u.* FROM users u
LEFT JOIN workspace_members wm ON wm.user_id = u.id
WHERE u.kind = 'service_account'
  AND (u.home_workspace_id = ANY(@workspace_ids::uuid[]) OR wm.workspace_id = ANY(@workspace_ids::uuid[]))
ORDER BY u.name;

-- name: UpdateServiceAccount :one
UPDATE users
SET name             = @name::text,
    description      = sqlc.narg(description)::text,
    auth_provider    = @auth_provider::text,
    auth_provider_id = sqlc.narg(auth_provider_id)::uuid,
    external_id      = sqlc.narg(external_id)::text,
    updated_at       = now()
WHERE id = @id::uuid AND kind = 'service_account'
RETURNING *;

-- name: GetServiceAccountByClient :one
-- The active service account a client-credentials token of a provider
-- authenticates as.
SELECT * FROM users
WHERE kind = 'service_account' AND auth_provider = 'oidc'
  AND auth_provider_id = @auth_provider_id::uuid AND external_id = @external_id::text
  AND status = 'active';

-- name: DeleteServiceAccount :execrows
DELETE FROM users WHERE id = @id::uuid AND kind = 'service_account';

-- name: ListAdminWorkspaceIDs :many
-- The workspaces the user administers (#653).
SELECT w.id FROM workspaces w WHERE baleia_workspace_member_role(w.id, @user_id::uuid) = 'admin';
