package authz

import (
	"context"
	"errors"

	"github.com/galileostudio/baleia/internal/db"
)

// WorkspaceRole is what a caller may do in a workspace (#653, docs/adr/0016).
// The order matters: a higher role includes everything a lower one can do.
// Data access inside the workspace's catalogs is never decided here: the
// catalog manager decides it (docs/adr/0015).
type WorkspaceRole int

const (
	// WorkspaceNone: not a member; the workspace is invisible (404).
	WorkspaceNone WorkspaceRole = iota
	// WorkspaceViewer sees content, runs saved queries, Explore and ad-hoc SQL.
	WorkspaceViewer
	// WorkspaceEditor also creates and edits content: saved queries and
	// scripts, metrics, semantic models and docs, retrievers.
	WorkspaceEditor
	// WorkspaceAdmin also manages the workspace: members and their roles,
	// linked catalogs, default target, spaces, settings, deletion.
	WorkspaceAdmin
)

func (r WorkspaceRole) String() string {
	switch r {
	case WorkspaceViewer:
		return "viewer"
	case WorkspaceEditor:
		return "editor"
	case WorkspaceAdmin:
		return "admin"
	default:
		return ""
	}
}

// ParseWorkspaceRole reads a stored or requested role; WorkspaceNone for
// anything else.
func ParseWorkspaceRole(s string) WorkspaceRole {
	switch s {
	case "viewer":
		return WorkspaceViewer
	case "editor":
		return WorkspaceEditor
	case "admin":
		return WorkspaceAdmin
	default:
		return WorkspaceNone
	}
}

// WorkspaceRole is the caller's role in the workspace. A global admin is a
// workspace admin everywhere. An agent acts with its owner's role, and only
// in the workspaces it is bound to.
func (c *Checker) WorkspaceRole(ctx context.Context, p Principal, wsID db.UUID) (WorkspaceRole, error) {
	if _, err := c.q.GetWorkspaceByID(ctx, wsID); err != nil {
		if errors.Is(err, db.ErrNoRows) {
			return WorkspaceNone, nil
		}
		return WorkspaceNone, err
	}
	if p.IsAgent() {
		bound, err := c.q.AgentWorkspaceBound(ctx, db.AgentWorkspaceBoundParams{AgentID: p.AgentID, WorkspaceID: wsID})
		if err != nil || !bound {
			return WorkspaceNone, err
		}
	}
	if p.IsAdmin() {
		return WorkspaceAdmin, nil
	}
	role, err := c.q.GetWorkspaceMemberRole(ctx, db.GetWorkspaceMemberRoleParams{ID: wsID, UserID: p.UserID})
	if err != nil {
		return WorkspaceNone, err
	}
	return ParseWorkspaceRole(role), nil
}

// RequireWorkspaceRole returns ErrNotFound for a non-member (no existence
// oracle) and ErrForbidden below min, reporting refusals like every check.
func (c *Checker) RequireWorkspaceRole(ctx context.Context, p Principal, wsID db.UUID, minRole WorkspaceRole) (role WorkspaceRole, err error) {
	defer c.observe(ctx, "workspace", wsID, &err)
	if role, err = c.WorkspaceRole(ctx, p, wsID); err != nil {
		return WorkspaceNone, err
	}
	switch {
	case role == WorkspaceNone:
		return WorkspaceNone, ErrNotFound
	case role < minRole:
		return role, ErrForbidden
	}
	return role, nil
}
