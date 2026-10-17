-- Spaces (#40): named containers inside a workspace with their own access list.
-- The effective role is computed by baleia_effective_space_role so listing and
-- single-space checks share one rule (#16 matrix).

-- name: CreateSpace :one
INSERT INTO spaces (workspace_id, name, slug, description, visibility, default_role, created_by)
VALUES (
    @workspace_id::uuid, @name::text, @slug::text, sqlc.narg(description)::text,
    @visibility::text, @default_role::text, sqlc.narg(created_by)::uuid
)
RETURNING *;

-- name: GetSpace :one
SELECT * FROM spaces WHERE workspace_id = @workspace_id::uuid AND id = @id::uuid;

-- name: GetDefaultSpace :one
SELECT * FROM spaces WHERE workspace_id = @workspace_id::uuid AND is_default;

-- name: ListSpacesForPrincipal :many
-- Every space of the workspace where the caller has at least viewer, with the
-- role. A space with no role is invisible (absent), never 403.
SELECT s.*, baleia_effective_space_role(s.workspace_id, s.id, @user_id::uuid)::text AS my_role
FROM spaces s
WHERE s.workspace_id = @workspace_id::uuid
  AND baleia_effective_space_role(s.workspace_id, s.id, @user_id::uuid) IS NOT NULL
ORDER BY s.is_default DESC, s.name;

-- name: GetEffectiveSpaceRole :one
-- Empty string when the caller has no role (the space is invisible).
SELECT COALESCE(
    baleia_effective_space_role(@workspace_id::uuid, @space_id::uuid, @user_id::uuid), ''
)::text AS role;

-- name: SpaceSlugExists :one
SELECT EXISTS (
    SELECT 1 FROM spaces WHERE workspace_id = @workspace_id::uuid AND slug = @slug::text
) AS exists;

-- name: UpdateSpace :one
UPDATE spaces
SET name = @name::text,
    description = sqlc.narg(description)::text,
    visibility = @visibility::text,
    default_role = @default_role::text,
    updated_at = now()
WHERE workspace_id = @workspace_id::uuid AND id = @id::uuid
RETURNING *;

-- name: DeleteSpace :execrows
DELETE FROM spaces
WHERE workspace_id = @workspace_id::uuid AND id = @id::uuid AND NOT is_default;

-- name: CountSpaceContent :one
SELECT
    (SELECT count(*) FROM query_folders WHERE space_id = @space_id::uuid) +
    (SELECT count(*) FROM saved_queries WHERE space_id = @space_id::uuid) +
    (SELECT count(*) FROM semantic_metrics WHERE space_id = @space_id::uuid) AS n;

-- name: SetSpaceMember :one
INSERT INTO space_members (space_id, principal_type, principal_id, role, granted_by)
VALUES (@space_id::uuid, @principal_type::text, @principal_id::uuid, @role::text, sqlc.narg(granted_by)::uuid)
ON CONFLICT (space_id, principal_type, principal_id) DO UPDATE
SET role = EXCLUDED.role, granted_by = EXCLUDED.granted_by, granted_at = now()
RETURNING *;

-- name: DeleteSpaceMember :execrows
DELETE FROM space_members
WHERE space_id = @space_id::uuid
  AND principal_type = @principal_type::text
  AND principal_id = @principal_id::uuid;

-- name: ListSpaceMembers :many
SELECT * FROM space_members WHERE space_id = @space_id::uuid ORDER BY principal_type, principal_id;

-- name: CountSpaceContentKinds :one
SELECT
    (SELECT count(*) FROM saved_queries WHERE space_id = @space_id::uuid)::bigint AS saved_queries,
    (SELECT count(*) FROM semantic_metrics WHERE space_id = @space_id::uuid)::bigint AS semantic_metrics,
    (SELECT count(*) FROM query_folders WHERE space_id = @space_id::uuid)::bigint AS folders;
