package repo

import (
	"context"
	"time"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/store/model"
	"github.com/jackc/pgx/v5/pgtype"
	"gorm.io/gorm"
)

// APIKeys persists a user's personal API keys.
type APIKeys struct{ db *gorm.DB }

// NewAPIKeys builds the API-key store.
func NewAPIKeys(db *gorm.DB) *APIKeys { return &APIKeys{db: db} }

// ListAPIKeysByUser returns the user's keys, oldest first.
func (s *APIKeys) ListAPIKeysByUser(ctx context.Context, userID pgtype.UUID) ([]db.ApiKey, error) {
	var rows []model.APIKey
	if err := s.db.WithContext(ctx).
		Where("user_id = ?", UUIDString(userID)).
		Order("created_at").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]db.ApiKey, len(rows))
	for i, row := range rows {
		out[i] = toDBApiKey(row)
	}
	return out, nil
}

// CreateAPIKey inserts a key and returns the stored row.
func (s *APIKeys) CreateAPIKey(ctx context.Context, arg db.CreateAPIKeyParams) (db.ApiKey, error) {
	row := model.APIKey{
		ID:        model.UUID(NewUUID()),
		UserID:    model.UUID(UUIDString(arg.UserID)),
		Name:      arg.Name,
		KeyHash:   model.Text(arg.KeyHash),
		Prefix:    model.Text(arg.Prefix),
		Scopes:    model.StringArray(arg.Scopes),
		ExpiresAt: TimePtr(arg.ExpiresAt),
		CreatedAt: time.Now().UTC(),
	}
	if err := s.db.WithContext(ctx).Create(&row).Error; err != nil {
		return db.ApiKey{}, err
	}
	return toDBApiKey(row), nil
}

// DeleteAPIKey removes the key only when it belongs to the user.
func (s *APIKeys) DeleteAPIKey(ctx context.Context, arg db.DeleteAPIKeyParams) error {
	return s.db.WithContext(ctx).
		Where("id = ? AND user_id = ?", UUIDString(arg.ID), UUIDString(arg.UserID)).
		Delete(&model.APIKey{}).Error
}

func toDBApiKey(k model.APIKey) db.ApiKey {
	return db.ApiKey{
		ID:         PGUUID(string(k.ID)),
		UserID:     PGUUID(string(k.UserID)),
		Name:       k.Name,
		KeyHash:    string(k.KeyHash),
		ExpiresAt:  PGTimePtr(k.ExpiresAt),
		CreatedAt:  PGTime(k.CreatedAt),
		Prefix:     string(k.Prefix),
		Scopes:     []string(k.Scopes),
		LastUsedAt: PGTimePtr(k.LastUsedAt),
	}
}
