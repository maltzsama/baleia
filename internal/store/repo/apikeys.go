package repo

import (
	"context"
	"errors"
	"time"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/store/model"
	"gorm.io/gorm"
)

// APIKeys persists a user's personal API keys.
type APIKeys struct{ db *gorm.DB }

// NewAPIKeys builds the API-key store.
func NewAPIKeys(db *gorm.DB) *APIKeys { return &APIKeys{db: db} }

// ListAPIKeysByUser returns the user's personal keys, oldest first.
func (s *APIKeys) ListAPIKeysByUser(ctx context.Context, userID db.UUID) ([]db.ApiKey, error) {
	var rows []model.APIKey
	if err := s.db.WithContext(ctx).
		Where("user_id = ? AND kind = ?", UUIDString(userID), "user").
		Order("created_at DESC").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]db.ApiKey, len(rows))
	for i, row := range rows {
		out[i] = toDBApiKey(row)
	}
	return out, nil
}

// CreateAPIKey inserts a key and returns the stored row. Kind "" is a personal
// key; a BI key carries its workspace and engine (ADR 0017).
func (s *APIKeys) CreateAPIKey(ctx context.Context, arg db.CreateAPIKeyParams) (db.ApiKey, error) {
	kind := arg.Kind
	if kind == "" {
		kind = "user"
	}
	row := model.APIKey{
		ID:          model.UUID(NewUUID()),
		UserID:      model.UUID(UUIDString(arg.UserID)),
		Kind:        model.Text(kind),
		WorkspaceID: modelUUIDPtr(arg.WorkspaceID),
		EngineID:    modelUUIDPtr(arg.EngineID),
		Name:        arg.Name,
		KeyHash:     model.Text(arg.KeyHash),
		Prefix:      model.Text(arg.Prefix),
		Scopes:      model.StringArray(arg.Scopes),
		ExpiresAt:   TimePtr(arg.ExpiresAt),
		CreatedAt:   time.Now().UTC(),
	}
	if err := s.db.WithContext(ctx).Create(&row).Error; err != nil {
		return db.ApiKey{}, err
	}
	return toDBApiKey(row), nil
}

// DeleteAPIKey removes a personal key only when it belongs to the user.
func (s *APIKeys) DeleteAPIKey(ctx context.Context, arg db.DeleteAPIKeyParams) error {
	return s.db.WithContext(ctx).
		Where("id = ? AND user_id = ? AND kind = ?", UUIDString(arg.ID), UUIDString(arg.UserID), "user").
		Delete(&model.APIKey{}).Error
}

// ListBIKeysByWorkspace returns every workspace-bound BI key, newest first.
func (s *APIKeys) ListBIKeysByWorkspace(ctx context.Context, workspaceID db.UUID) ([]db.ApiKey, error) {
	return s.listBIKeys(ctx, "workspace_id = ?", UUIDString(workspaceID))
}

// ListBIKeysByWorkspaceOwner returns the workspace's BI keys owned by userID.
func (s *APIKeys) ListBIKeysByWorkspaceOwner(ctx context.Context, workspaceID, userID db.UUID) ([]db.ApiKey, error) {
	return s.listBIKeys(ctx, "workspace_id = ? AND user_id = ?", UUIDString(workspaceID), UUIDString(userID))
}

func (s *APIKeys) listBIKeys(ctx context.Context, where string, args ...any) ([]db.ApiKey, error) {
	var rows []model.APIKey
	if err := s.db.WithContext(ctx).
		Where("kind = ? AND "+where, append([]any{"bi"}, args...)...).
		Order("created_at DESC").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]db.ApiKey, len(rows))
	for i, row := range rows {
		out[i] = toDBApiKey(row)
	}
	return out, nil
}

// GetBIKeyByID returns a workspace-bound BI key of the workspace or db.ErrNoRows.
func (s *APIKeys) GetBIKeyByID(ctx context.Context, id, workspaceID db.UUID) (db.ApiKey, error) {
	var row model.APIKey
	err := s.db.WithContext(ctx).
		Where("id = ? AND workspace_id = ? AND kind = ?", UUIDString(id), UUIDString(workspaceID), "bi").
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return db.ApiKey{}, db.ErrNoRows
	}
	if err != nil {
		return db.ApiKey{}, err
	}
	return toDBApiKey(row), nil
}

// DeleteBIKey removes a workspace-bound BI key and reports how many rows it
// deleted (0 when the key is not a BI key of the workspace).
func (s *APIKeys) DeleteBIKey(ctx context.Context, id, workspaceID db.UUID) (int64, error) {
	res := s.db.WithContext(ctx).
		Where("id = ? AND workspace_id = ? AND kind = ?", UUIDString(id), UUIDString(workspaceID), "bi").
		Delete(&model.APIKey{})
	return res.RowsAffected, res.Error
}

func toDBApiKey(k model.APIKey) db.ApiKey {
	return db.ApiKey{
		ID:          PGUUID(string(k.ID)),
		UserID:      PGUUID(string(k.UserID)),
		Kind:        string(k.Kind),
		WorkspaceID: uuidPtr(k.WorkspaceID),
		EngineID:    uuidPtr(k.EngineID),
		Name:        k.Name,
		KeyHash:     string(k.KeyHash),
		ExpiresAt:   PGTimePtr(k.ExpiresAt),
		CreatedAt:   PGTime(k.CreatedAt),
		Prefix:      string(k.Prefix),
		Scopes:      []string(k.Scopes),
		LastUsedAt:  PGTimePtr(k.LastUsedAt),
	}
}
