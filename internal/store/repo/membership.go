package repo

import (
	"context"
	"database/sql"
	"errors"

	"github.com/galileostudio/baleia/internal/store/model"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// WorkspaceRole is the Go replacement for the baleia_workspace_member_role()
// PL/pgSQL function removed by ADR 0020. It returns the user's effective role
// in the workspace, or "" when the user is not a member.
//
// The reads run in one repeatable-read transaction: ownership and membership
// can change between statements, and combining an old owner with a new
// membership would grant a role the user never had.
func (r *Repo) WorkspaceRole(ctx context.Context, workspaceID, userID string) (string, error) {
	var role string
	err := r.Users.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		role, err = workspaceRole(tx, workspaceID, userID)
		return err
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	return role, err
}

func workspaceRole(db *gorm.DB, workspaceID, userID string) (string, error) {
	var ws model.Workspace
	if err := db.Where("id = ?", workspaceID).First(&ws).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", nil
		}
		return "", err
	}
	return workspaceRoleFor(db, ws, userID)
}

// workspaceRoleFor is workspaceRole over an already-loaded workspace, so a
// caller listing workspaces does not fetch each row twice.
func workspaceRoleFor(db *gorm.DB, ws model.Workspace, userID string) (string, error) {
	var member model.WorkspaceMember
	memberErr := db.Where("workspace_id = ? AND user_id = ?", string(ws.ID), userID).First(&member).Error
	if memberErr != nil && !errors.Is(memberErr, gorm.ErrRecordNotFound) {
		return "", memberErr
	}
	hasMember := memberErr == nil

	if ws.Type == "personal" {
		if ws.OwnerUserID != nil && sameUUID(string(*ws.OwnerUserID), userID) {
			return "admin", nil
		}
		if hasMember {
			return string(member.Role), nil
		}
		return "", nil
	}

	var user model.User
	userErr := db.Where("id = ?", userID).First(&user).Error
	if userErr != nil && !errors.Is(userErr, gorm.ErrRecordNotFound) {
		return "", userErr
	}
	// A service account gets exactly the role it was granted.
	if userErr == nil && user.Kind == "service_account" {
		if hasMember {
			return string(member.Role), nil
		}
		return "", nil
	}

	if ws.OwnerTeamID == nil {
		return "", nil
	}
	var teamMember model.TeamMember
	teamErr := db.Where("team_id = ? AND user_id = ?", string(*ws.OwnerTeamID), userID).First(&teamMember).Error
	if teamErr != nil && !errors.Is(teamErr, gorm.ErrRecordNotFound) {
		return "", teamErr
	}
	if errors.Is(teamErr, gorm.ErrRecordNotFound) {
		return "", nil
	}
	if hasMember {
		return string(member.Role), nil
	}
	if teamMember.Role == "owner" {
		return "admin", nil
	}
	return "editor", nil
}

// sameUUID compares parsed UUID values, so an uppercase representation still
// matches the owner's canonical lowercase id.
func sameUUID(a, b string) bool {
	ua, errA := uuid.Parse(a)
	ub, errB := uuid.Parse(b)
	return errA == nil && errB == nil && ua == ub
}
