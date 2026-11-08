package repo

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"time"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/store/model"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ServiceAccounts persists non-human principals (#653) and their workspace
// membership. It is the GORM replacement for the service_accounts.sql queries
// (ADR 0020); db.Querier remains the sqlc fallback while other domains migrate.
type ServiceAccounts struct{ db *gorm.DB }

// ServiceAccountWrites is the transactional surface creating a service
// account needs: the account row and its membership in one transaction.
type ServiceAccountWrites interface {
	CreateServiceAccount(ctx context.Context, arg db.CreateServiceAccountParams) (db.User, error)
	UpsertWorkspaceMember(ctx context.Context, arg db.UpsertWorkspaceMemberParams) (int64, error)
}

// NewServiceAccounts builds the service-account store.
func NewServiceAccounts(db *gorm.DB) *ServiceAccounts { return &ServiceAccounts{db: db} }

// Tx runs fn in one transaction over the service-account store.
func (s *ServiceAccounts) Tx(ctx context.Context, fn func(ServiceAccountWrites) error) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(&ServiceAccounts{db: tx})
	})
}

// GetUserByID returns one account or pgx.ErrNoRows.
func (s *ServiceAccounts) GetUserByID(ctx context.Context, id pgtype.UUID) (db.User, error) {
	return NewUsers(s.db).GetUserByID(ctx, id)
}

// GetAuthProviderByID returns one provider or pgx.ErrNoRows.
func (s *ServiceAccounts) GetAuthProviderByID(ctx context.Context, id pgtype.UUID) (db.AuthProvider, error) {
	return NewProviders(s.db).GetAuthProviderByID(ctx, id)
}

// DeleteUserIDPToken removes the account's identity-provider token.
func (s *ServiceAccounts) DeleteUserIDPToken(ctx context.Context, userID pgtype.UUID) error {
	return NewUsers(s.db).DeleteUserIDPToken(ctx, userID)
}

// GetServiceAccount returns one service account or pgx.ErrNoRows.
func (s *ServiceAccounts) GetServiceAccount(ctx context.Context, id pgtype.UUID) (db.User, error) {
	if !id.Valid {
		return db.User{}, pgx.ErrNoRows
	}
	var row model.User
	err := s.db.WithContext(ctx).
		Where("id = ? AND kind = ?", UUIDString(id), "service_account").
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return db.User{}, pgx.ErrNoRows
	}
	if err != nil {
		return db.User{}, err
	}
	return toDBUser(row), nil
}

// ListServiceAccounts returns every service account, by name.
func (s *ServiceAccounts) ListServiceAccounts(ctx context.Context) ([]db.User, error) {
	var rows []model.User
	if err := s.db.WithContext(ctx).
		Where("kind = ?", "service_account").
		Order("name").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	return toDBUsers(rows), nil
}

// ListServiceAccountsOfWorkspaces returns the service accounts created in, or
// members of, the given workspaces, by name. Two model queries replace the
// sqlc LEFT JOIN + DISTINCT so the result is identical on every backend.
func (s *ServiceAccounts) ListServiceAccountsOfWorkspaces(ctx context.Context, workspaceIDs []pgtype.UUID) ([]db.User, error) {
	ids := make([]string, len(workspaceIDs))
	for i, id := range workspaceIDs {
		ids[i] = UUIDString(id)
	}
	if len(ids) == 0 {
		return nil, nil
	}
	var home []model.User
	if err := s.db.WithContext(ctx).
		Where("kind = ? AND home_workspace_id IN ?", "service_account", ids).
		Find(&home).Error; err != nil {
		return nil, err
	}
	var members []model.User
	memberIDs := s.db.Model(&model.WorkspaceMember{}).Select("user_id").Where("workspace_id IN ?", ids)
	if err := s.db.WithContext(ctx).
		Where("kind = ? AND id IN (?)", "service_account", memberIDs).
		Find(&members).Error; err != nil {
		return nil, err
	}
	seen := make(map[model.UUID]bool, len(home)+len(members))
	out := make([]model.User, 0, len(home)+len(members))
	for _, list := range [][]model.User{home, members} {
		for _, row := range list {
			if seen[row.ID] {
				continue
			}
			seen[row.ID] = true
			out = append(out, row)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return toDBUsers(out), nil
}

// ListAdminWorkspaceIDs returns the workspaces the user administers (#653).
// The role is the Go replacement for baleia_workspace_member_role, read in one
// repeatable-read transaction like WorkspaceRole.
func (s *ServiceAccounts) ListAdminWorkspaceIDs(ctx context.Context, userID pgtype.UUID) ([]pgtype.UUID, error) {
	var out []pgtype.UUID
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var rows []model.Workspace
		if err := tx.Order("name").Find(&rows).Error; err != nil {
			return err
		}
		for _, ws := range rows {
			role, err := workspaceRoleFor(tx, ws, UUIDString(userID))
			if err != nil {
				return err
			}
			if role == "admin" {
				out = append(out, PGUUID(string(ws.ID)))
			}
		}
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	return out, err
}

// CreateServiceAccount inserts a service account: a user of kind
// 'service_account', role 'member', no password. IDs are generated in the
// application so the insert works on every backend.
func (s *ServiceAccounts) CreateServiceAccount(ctx context.Context, arg db.CreateServiceAccountParams) (db.User, error) {
	now := time.Now().UTC()
	row := model.User{
		ID:              model.UUID(NewUUID()),
		Name:            arg.Name,
		Email:           model.Text(arg.Email),
		Role:            "member",
		AuthProvider:    model.Text(arg.AuthProvider),
		AuthProviderID:  modelUUIDPtr(arg.AuthProviderID),
		ExternalID:      modelTextPtr(arg.ExternalID),
		HomeWorkspaceID: modelUUIDPtr(arg.HomeWorkspaceID),
		Description:     arg.Description,
		Status:          "active",
		Kind:            "service_account",
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := s.db.WithContext(ctx).Create(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return db.User{}, &pgconn.PgError{Code: uniqueViolation, Message: "duplicate key value violates unique constraint"}
		}
		return db.User{}, err
	}
	return toDBUser(row), nil
}

// UpdateServiceAccount edits a service account and returns the row. The target
// row is locked for the update and the read, so a concurrent update or delete
// cannot change the identity-provider link or vanish between the two.
func (s *ServiceAccounts) UpdateServiceAccount(ctx context.Context, arg db.UpdateServiceAccountParams) (db.User, error) {
	var out db.User
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row model.User
		found := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND kind = ?", UUIDString(arg.ID), "service_account").
			First(&row)
		if errors.Is(found.Error, gorm.ErrRecordNotFound) {
			return pgx.ErrNoRows
		}
		if found.Error != nil {
			return found.Error
		}
		upd := tx.Model(&model.User{}).
			Where("id = ?", UUIDString(arg.ID)).
			Updates(map[string]any{
				"name":             arg.Name,
				"description":      arg.Description,
				"auth_provider":    arg.AuthProvider,
				"auth_provider_id": modelUUIDPtr(arg.AuthProviderID),
				"external_id":      modelTextPtr(arg.ExternalID),
				"updated_at":       time.Now().UTC(),
			})
		if upd.Error != nil {
			if errors.Is(upd.Error, gorm.ErrDuplicatedKey) {
				return &pgconn.PgError{Code: uniqueViolation, Message: "duplicate key value violates unique constraint"}
			}
			return upd.Error
		}
		if err := tx.Where("id = ?", UUIDString(arg.ID)).First(&row).Error; err != nil {
			return err
		}
		out = toDBUser(row)
		return nil
	})
	if err != nil {
		return db.User{}, err
	}
	return out, nil
}

// DeleteServiceAccount removes a service account and returns the rows.
func (s *ServiceAccounts) DeleteServiceAccount(ctx context.Context, id pgtype.UUID) (int64, error) {
	res := s.db.WithContext(ctx).
		Where("id = ? AND kind = ?", UUIDString(id), "service_account").
		Delete(&model.User{})
	return res.RowsAffected, res.Error
}

// UpsertWorkspaceMember writes a membership the caller may grant and returns
// the rows: 0 when the user cannot be a member. On a shared workspace a person
// needs the owning team; a service account joins directly (#653). The check
// and the write share one transaction; the PostgreSQL share lock the sqlc
// query took is not reproduced (ADR 0020 forbids SQL-specific locking), so a
// concurrent team removal is serialized by the transaction, not a row lock.
func (s *ServiceAccounts) UpsertWorkspaceMember(ctx context.Context, arg db.UpsertWorkspaceMemberParams) (int64, error) {
	var rows int64
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		allowed, err := (&ServiceAccounts{db: tx}).memberAllowed(ctx, arg.WorkspaceID, arg.UserID)
		if err != nil || !allowed {
			return err
		}
		now := time.Now().UTC()
		row := model.WorkspaceMember{
			WorkspaceID: model.UUID(UUIDString(arg.WorkspaceID)),
			UserID:      model.UUID(UUIDString(arg.UserID)),
			Role:        arg.Role,
			GrantedBy:   modelUUIDPtr(arg.GrantedBy),
			GrantedAt:   now,
			Source:      "manual",
		}
		res := tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "workspace_id"}, {Name: "user_id"}},
			DoUpdates: clause.Assignments(map[string]any{
				"role":       arg.Role,
				"granted_by": modelUUIDPtr(arg.GrantedBy),
				"granted_at": now,
				"source":     "manual",
			}),
		}).Create(&row)
		rows = res.RowsAffected
		return res.Error
	})
	return rows, err
}

// memberAllowed mirrors the sqlc query's allowed set: a personal workspace, a
// shared workspace whose owning team contains the user, or a service account
// joined directly to a shared workspace.
func (s *ServiceAccounts) memberAllowed(ctx context.Context, workspaceID, userID pgtype.UUID) (bool, error) {
	var ws model.Workspace
	err := s.db.WithContext(ctx).Where("id = ?", UUIDString(workspaceID)).First(&ws).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if ws.Type == "personal" {
		return true, nil
	}
	if ws.OwnerTeamID != nil {
		var count int64
		if err := s.db.WithContext(ctx).Model(&model.TeamMember{}).
			Where("team_id = ? AND user_id = ?", string(*ws.OwnerTeamID), UUIDString(userID)).
			Count(&count).Error; err != nil {
			return false, err
		}
		if count > 0 {
			return true, nil
		}
	}
	var user model.User
	err = s.db.WithContext(ctx).Select("kind").Where("id = ?", UUIDString(userID)).First(&user).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return user.Kind == "service_account", nil
}

// toDBUsers converts a slice of model users to the sqlc shape.
func toDBUsers(rows []model.User) []db.User {
	out := make([]db.User, len(rows))
	for i, row := range rows {
		out[i] = toDBUser(row)
	}
	return out
}
