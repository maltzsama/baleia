package repo

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/store/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// TxProvision runs fn in one transaction over a Repo bound to it: a directory
// pass applies suspensions, reinstatements, roles, teams and the synced mark
// as one unit.
func (r *Repo) TxProvision(ctx context.Context, fn func(*Repo) error) error {
	return r.Users.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(New(tx))
	})
}

// ApplyGroupWorkspaceRoles applies a sign-in's group-derived workspace roles
// (#653): it writes the idp_group memberships the user's teams grant and
// removes the idp_group ones they no longer grant. A manual role an admin set
// is never touched, a personal workspace is never reached, and a shared
// workspace whose owning team does not contain the user is skipped.
//
// This is the Go replacement for baleia_apply_group_workspace_roles (ADR 0020
// removes the PL/pgSQL function and its advisory lock). The reads and writes
// share one repeatable-read transaction; an insert that races another sign-in
// falls back to an update that still respects the manual source.
func (r *Repo) ApplyGroupWorkspaceRoles(ctx context.Context, arg db.ApplyGroupWorkspaceRolesParams) error {
	assignments := make([]struct {
		WorkspaceID string `json:"workspace_id"`
		Role        string `json:"role"`
	}, 0)
	if len(arg.Assignments) > 0 {
		if err := json.Unmarshal(arg.Assignments, &assignments); err != nil {
			return err
		}
	}
	userID := UUIDString(arg.UserID)
	return r.Users.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Lock the account row so concurrent sign-ins of the same user apply
		// their mappings in turn, as the removed advisory lock did. A
		// repeatable-read transaction would abort one of them instead.
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ?", userID).First(&model.User{}).Error; err != nil {
			return err
		}
		keep, err := grantedWorkspaces(tx, userID, assignments)
		if err != nil {
			return err
		}
		if err := writeGroupRoles(tx, userID, keep); err != nil {
			return err
		}
		return dropGroupRoles(tx, userID, keep)
	})
}

// grantedWorkspaces keeps the assignments whose workspace is shared, owned by
// a team the user belongs to, mapped to the group's role.
func grantedWorkspaces(tx *gorm.DB, userID string, assignments []struct {
	WorkspaceID string `json:"workspace_id"`
	Role        string `json:"role"`
}) (map[string]string, error) {
	keep := make(map[string]string, len(assignments))
	for _, a := range assignments {
		var ws model.Workspace
		err := tx.Where("id = ? AND type = ?", a.WorkspaceID, "shared").First(&ws).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if ws.OwnerTeamID == nil {
			continue
		}
		var count int64
		if err := tx.Model(&model.TeamMember{}).
			Where("team_id = ? AND user_id = ?", string(*ws.OwnerTeamID), userID).
			Count(&count).Error; err != nil {
			return nil, err
		}
		if count == 0 {
			continue
		}
		keep[UUIDString(PGUUID(a.WorkspaceID))] = a.Role
	}
	return keep, nil
}

// writeGroupRoles inserts or updates the kept memberships as idp_group,
// leaving a manual role alone. A concurrent insert is handled by DoNothing and
// the follow-up update.
func writeGroupRoles(tx *gorm.DB, userID string, keep map[string]string) error {
	for wsID, role := range keep {
		now := time.Now().UTC()
		row := model.WorkspaceMember{
			WorkspaceID: model.UUID(wsID),
			UserID:      model.UUID(userID),
			Role:        role,
			GrantedAt:   now,
			Source:      "idp_group",
		}
		res := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "workspace_id"}, {Name: "user_id"}},
			DoNothing: true,
		}).Create(&row)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected > 0 {
			continue
		}
		// The membership already exists: update it only when a group set it.
		if err := tx.Model(&model.WorkspaceMember{}).
			Where("workspace_id = ? AND user_id = ? AND source = ?", wsID, userID, "idp_group").
			Updates(map[string]any{"role": role, "granted_at": now}).Error; err != nil {
			return err
		}
	}
	return nil
}

// dropGroupRoles removes the idp_group memberships no longer granted.
func dropGroupRoles(tx *gorm.DB, userID string, keep map[string]string) error {
	var stale []string
	if err := tx.Model(&model.WorkspaceMember{}).
		Where("user_id = ? AND source = ?", userID, "idp_group").
		Pluck("workspace_id", &stale).Error; err != nil {
		return err
	}
	remove := make([]string, 0, len(stale))
	for _, id := range stale {
		if _, ok := keep[id]; !ok {
			remove = append(remove, id)
		}
	}
	if len(remove) == 0 {
		return nil
	}
	return tx.Where("user_id = ? AND source = ? AND workspace_id IN ?", userID, "idp_group", remove).
		Delete(&model.WorkspaceMember{}).Error
}

// GetUserByExternalID returns the account linked to a provider subject, or
// db.ErrNoRows.
func (s *Users) GetUserByExternalID(ctx context.Context, arg db.GetUserByExternalIDParams) (db.User, error) {
	if !arg.AuthProviderID.Valid || arg.ExternalID == nil {
		return db.User{}, db.ErrNoRows
	}
	var row model.User
	err := s.db.WithContext(ctx).
		Where("auth_provider_id = ? AND external_id = ?", UUIDString(arg.AuthProviderID), *arg.ExternalID).
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return db.User{}, db.ErrNoRows
	}
	if err != nil {
		return db.User{}, err
	}
	return toDBUser(row), nil
}

// ListUsersByAuthProvider returns the accounts linked to a provider, by name.
func (s *Users) ListUsersByAuthProvider(ctx context.Context, authProviderID db.UUID) ([]db.User, error) {
	var rows []model.User
	if err := s.db.WithContext(ctx).
		Where("auth_provider_id = ?", UUIDString(authProviderID)).
		Order("name").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]db.User, len(rows))
	for i, row := range rows {
		out[i] = toDBUser(row)
	}
	return out, nil
}

// MarkDirectorySynced stamps the account's last successful directory pass.
func (s *Users) MarkDirectorySynced(ctx context.Context, id db.UUID) error {
	now := time.Now().UTC()
	return s.db.WithContext(ctx).Model(&model.User{}).
		Where("id = ?", UUIDString(id)).
		Updates(map[string]any{"directory_synced_at": now, "updated_at": now}).Error
}

// ReinstateDirectoryUser lifts only the directory's own suspension (#257).
func (s *Users) ReinstateDirectoryUser(ctx context.Context, id db.UUID) error {
	return s.db.WithContext(ctx).Model(&model.User{}).
		Where("id = ? AND status = ? AND suspended_reason = ?", UUIDString(id), "suspended", "directory").
		Updates(map[string]any{
			"status":           "active",
			"suspended_at":     nil,
			"suspended_reason": nil,
			"updated_at":       time.Now().UTC(),
		}).Error
}

// CreateDirectoryReconcileRun opens a pass and returns its row.
func (s *Users) CreateDirectoryReconcileRun(ctx context.Context, providerID db.UUID) (db.DirectoryReconcileRun, error) {
	row := model.DirectoryReconcileRun{
		ID:         model.UUID(NewUUID()),
		ProviderID: model.UUID(UUIDString(providerID)),
		StartedAt:  time.Now().UTC(),
		Actions:    model.JSON("[]"),
	}
	if err := s.db.WithContext(ctx).Create(&row).Error; err != nil {
		return db.DirectoryReconcileRun{}, err
	}
	return toDBDirectoryReconcileRun(row), nil
}

// FinishDirectoryReconcileRun records the pass outcome and actions.
func (s *Users) FinishDirectoryReconcileRun(ctx context.Context, arg db.FinishDirectoryReconcileRunParams) error {
	return s.db.WithContext(ctx).Model(&model.DirectoryReconcileRun{}).
		Where("id = ?", UUIDString(arg.ID)).
		Updates(map[string]any{
			"finished_at": time.Now().UTC(),
			"outcome":     arg.Outcome,
			"seen":        arg.Seen,
			"suspended":   arg.Suspended,
			"reinstated":  arg.Reinstated,
			"error":       arg.Error,
			"actions":     model.JSON(arg.Actions),
		}).Error
}

// UpsertTeamMember adds the user to the team or changes their role.
func (s *Teams) UpsertTeamMember(ctx context.Context, arg db.UpsertTeamMemberParams) error {
	row := model.TeamMember{
		ID:       model.UUID(NewUUID()),
		TeamID:   model.UUID(UUIDString(arg.TeamID)),
		UserID:   model.UUID(UUIDString(arg.UserID)),
		Role:     model.Text(arg.Role),
		JoinedAt: time.Now().UTC(),
	}
	return s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "team_id"}, {Name: "user_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"role"}),
	}).Create(&row).Error
}

// ListEnabledLDAPProviders returns the enabled LDAP providers, by name.
func (s *Providers) ListEnabledLDAPProviders(ctx context.Context) ([]db.AuthProvider, error) {
	var rows []model.AuthProvider
	if err := s.db.WithContext(ctx).
		Where("enabled = ? AND type = ?", true, "ldap").
		Order("name").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]db.AuthProvider, len(rows))
	for i, row := range rows {
		out[i] = toDBAuthProvider(row)
	}
	return out, nil
}
