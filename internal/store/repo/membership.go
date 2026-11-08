package repo

import (
	"context"
	"errors"

	"github.com/galileostudio/baleia/internal/store/model"
	"gorm.io/gorm"
)

// WorkspaceRole is the Go replacement for the baleia_workspace_member_role()
// PL/pgSQL function removed by ADR 0020. It returns the user's effective role
// in the workspace, or "" when the user is not a member.
func (r *Repo) WorkspaceRole(ctx context.Context, workspaceID, userID string) (string, error) {
	db := r.Users.db.WithContext(ctx)

	var ws model.Workspace
	if err := db.Where("id = ?", workspaceID).First(&ws).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", nil
		}
		return "", err
	}

	var member model.WorkspaceMember
	memberErr := db.Where("workspace_id = ? AND user_id = ?", workspaceID, userID).First(&member).Error
	if memberErr != nil && !errors.Is(memberErr, gorm.ErrRecordNotFound) {
		return "", memberErr
	}
	hasMember := memberErr == nil

	if ws.Type == "personal" {
		if ws.OwnerUserID != nil && string(*ws.OwnerUserID) == userID {
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
