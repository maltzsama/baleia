package repo

import (
	"context"
	"errors"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/store/model"
	"gorm.io/gorm"
)

// ListWorkspaceMembers lists the members of a workspace with their effective
// role (#653). The role is resolved in Go from the workspace, its owning
// team's members and the explicit grants, replacing the
// baleia_workspace_member_role() PL/pgSQL function removed by ADR 0020.
func (r *Repo) ListWorkspaceMembers(ctx context.Context, workspaceID db.UUID) ([]db.ListWorkspaceMembersRow, error) {
	if !workspaceID.Valid {
		return nil, nil
	}
	var out []db.ListWorkspaceMembersRow
	err := r.Users.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var ws model.Workspace
		err := tx.Where("id = ?", UUIDString(workspaceID)).First(&ws).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		memberIDs := map[string]bool{}
		teamRoles := map[string]string{}
		if ws.Type == "shared" && ws.OwnerTeamID != nil {
			var team []model.TeamMember
			if err := tx.Where("team_id = ?", string(*ws.OwnerTeamID)).Find(&team).Error; err != nil {
				return err
			}
			for _, member := range team {
				teamRoles[string(member.UserID)] = string(member.Role)
				memberIDs[string(member.UserID)] = true
			}
		}
		var explicit []model.WorkspaceMember
		if err := tx.Where("workspace_id = ?", string(ws.ID)).Find(&explicit).Error; err != nil {
			return err
		}
		explicitRoles := make(map[string]string, len(explicit))
		for _, member := range explicit {
			explicitRoles[string(member.UserID)] = member.Role
			memberIDs[string(member.UserID)] = true
		}
		if ws.OwnerUserID != nil {
			memberIDs[string(*ws.OwnerUserID)] = true
		}
		ids := make([]string, 0, len(memberIDs))
		for id := range memberIDs {
			ids = append(ids, id)
		}
		if len(ids) == 0 {
			return nil
		}
		var users []model.User
		if err := tx.Where("id IN ?", ids).Order("name").Find(&users).Error; err != nil {
			return err
		}
		for _, user := range users {
			userID := string(user.ID)
			role := effectiveMemberRole(ws, user, teamRoles[userID], explicitRoles[userID])
			if role == "" {
				continue
			}
			source := "shared"
			switch {
			case ws.Type == "shared":
				source = "team"
			case ws.OwnerUserID != nil && sameUUID(string(*ws.OwnerUserID), userID):
				source = "owner"
			}
			_, roleSet := explicitRoles[userID]
			out = append(out, db.ListWorkspaceMembersRow{
				User: toDBUser(user), Role: role, Source: source, RoleSet: roleSet,
			})
		}
		return nil
	})
	return out, err
}

// effectiveMemberRole mirrors workspaceRoleFor without extra reads. A
// personal workspace's owner is its admin and an explicit grant applies. On a
// shared workspace a service account only has the role it was granted, while
// a human must still be in the owning team: a stale grant for a former team
// member grants nothing.
func effectiveMemberRole(ws model.Workspace, user model.User, teamRole, explicitRole string) string {
	if ws.Type == "personal" {
		if ws.OwnerUserID != nil && sameUUID(string(*ws.OwnerUserID), string(user.ID)) {
			return "admin"
		}
		return explicitRole
	}
	if user.Kind == "service_account" {
		return explicitRole
	}
	if teamRole == "" {
		return ""
	}
	if explicitRole != "" {
		return explicitRole
	}
	if teamRole == "owner" {
		return "admin"
	}
	return "editor"
}

// DeleteWorkspaceMember removes an explicitly granted role (#653).
func (r *Repo) DeleteWorkspaceMember(ctx context.Context, arg db.DeleteWorkspaceMemberParams) (int64, error) {
	res := r.Users.db.WithContext(ctx).
		Where("workspace_id = ? AND user_id = ?", UUIDString(arg.WorkspaceID), UUIDString(arg.UserID)).
		Delete(&model.WorkspaceMember{})
	return res.RowsAffected, res.Error
}

// UpsertWorkspaceMember writes a membership the caller may grant (#653) on
// the GORM path, sharing the service-account implementation.
func (r *Repo) UpsertWorkspaceMember(ctx context.Context, arg db.UpsertWorkspaceMemberParams) (int64, error) {
	return NewServiceAccounts(r.Users.db).UpsertWorkspaceMember(ctx, arg)
}

// GetServiceAccount returns one service account or db.ErrNoRows.
func (r *Repo) GetServiceAccount(ctx context.Context, id db.UUID) (db.User, error) {
	return NewServiceAccounts(r.Users.db).GetServiceAccount(ctx, id)
}
