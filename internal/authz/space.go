package authz

import (
	"context"
	"errors"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// SpaceRole is a caller's effective role in a space (#40). The order matters:
// a higher role includes everything a lower one can do.
type SpaceRole int

const (
	// SpaceNone means the space is invisible to the caller (404, never 403).
	SpaceNone SpaceRole = iota
	SpaceViewer
	SpaceEditor
	SpaceManager
)

func (r SpaceRole) String() string {
	switch r {
	case SpaceViewer:
		return "viewer"
	case SpaceEditor:
		return "editor"
	case SpaceManager:
		return "manager"
	default:
		return ""
	}
}

func spaceRoleFromString(s string) SpaceRole {
	switch s {
	case "viewer":
		return SpaceViewer
	case "editor":
		return SpaceEditor
	case "manager":
		return SpaceManager
	default:
		return SpaceNone
	}
}

// EffectiveSpaceRole computes the role in one SQL round trip: a global admin or
// workspace owner is a manager, a workspace member gets the space's default
// role when it is workspace-visible, and explicit user/team entries may raise
// it. Anyone else has no role, even with a stale entry (the workspace has to be
// visible first). SpaceNone means the space is invisible.
func (c *Checker) EffectiveSpaceRole(ctx context.Context, p Principal, workspaceID, spaceID pgtype.UUID) (SpaceRole, error) {
	role, err := c.q.GetEffectiveSpaceRole(ctx, db.GetEffectiveSpaceRoleParams{
		WorkspaceID: workspaceID,
		SpaceID:     spaceID,
		UserID:      p.UserID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return SpaceNone, nil
		}
		return SpaceNone, err
	}
	return spaceRoleFromString(role), nil
}

// RequireSpace returns ErrNotFound when the caller has no role (no existence
// oracle) and ErrForbidden when the role is below min. It reports refusals
// through the Checker's hooks like every other check.
func (c *Checker) RequireSpace(ctx context.Context, p Principal, workspaceID, spaceID pgtype.UUID, min SpaceRole) (SpaceRole, error) {
	role, err := c.EffectiveSpaceRole(ctx, p, workspaceID, spaceID)
	if err != nil {
		return SpaceNone, err
	}
	switch {
	case role == SpaceNone:
		err := ErrNotFound
		c.observe(ctx, "space", spaceID, &err)
		return SpaceNone, err
	case role < min:
		err := ErrForbidden
		c.observe(ctx, "space", spaceID, &err)
		return role, err
	}
	return role, nil
}
