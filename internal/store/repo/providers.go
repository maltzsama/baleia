package repo

import (
	"context"
	"errors"
	"time"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/identity/providers"
	"github.com/galileostudio/baleia/internal/store/model"
	"gorm.io/gorm"
)

// Providers persists OIDC and LDAP identity providers.
type Providers struct{ db *gorm.DB }

// NewProviders builds the identity-provider store.
func NewProviders(db *gorm.DB) *Providers { return &Providers{db: db} }

// Tx runs fn in one transaction over a Providers bound to it.
func (s *Providers) Tx(ctx context.Context, fn func(providers.TxStore) error) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(&Providers{db: tx})
	})
}

// GetAuthProviderByID returns one provider or db.ErrNoRows.
func (s *Providers) GetAuthProviderByID(ctx context.Context, id db.UUID) (db.AuthProvider, error) {
	if !id.Valid {
		return db.AuthProvider{}, db.ErrNoRows
	}
	var row model.AuthProvider
	err := s.db.WithContext(ctx).Where("id = ?", UUIDString(id)).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return db.AuthProvider{}, db.ErrNoRows
	}
	if err != nil {
		return db.AuthProvider{}, err
	}
	return toDBAuthProvider(row), nil
}

// ListAuthProviders returns the enabled providers, by name.
func (s *Providers) ListAuthProviders(ctx context.Context) ([]db.AuthProvider, error) {
	return s.list(ctx, true)
}

// ListAllAuthProviders returns every provider, by name.
func (s *Providers) ListAllAuthProviders(ctx context.Context) ([]db.AuthProvider, error) {
	return s.list(ctx, false)
}

func (s *Providers) list(ctx context.Context, enabledOnly bool) ([]db.AuthProvider, error) {
	q := s.db.WithContext(ctx).Order("name")
	if enabledOnly {
		q = q.Where("enabled = ?", true)
	}
	var rows []model.AuthProvider
	if err := q.Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]db.AuthProvider, len(rows))
	for i, row := range rows {
		out[i] = toDBAuthProvider(row)
	}
	return out, nil
}

// CountUsersByAuthProvider counts the accounts linked to the provider.
func (s *Providers) CountUsersByAuthProvider(ctx context.Context, authProviderID db.UUID) (int64, error) {
	var count int64
	err := s.db.WithContext(ctx).Model(&model.User{}).
		Where("auth_provider_id = ?", UUIDString(authProviderID)).
		Count(&count).Error
	return count, err
}

// CreateAuthProvider inserts a provider.
func (s *Providers) CreateAuthProvider(ctx context.Context, arg db.CreateAuthProviderParams) (db.AuthProvider, error) {
	now := time.Now().UTC()
	row := model.AuthProvider{
		ID:        model.UUID(NewUUID()),
		Name:      arg.Name,
		Type:      arg.Type,
		ConfigEnc: arg.ConfigEnc,
		Enabled:   arg.Enabled,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := s.db.WithContext(ctx).Create(&row).Error; err != nil {
		return db.AuthProvider{}, err
	}
	// The column defaults to true, so GORM omits the zero value on insert; a
	// provider created disabled needs its explicit false.
	if !arg.Enabled {
		if err := s.db.WithContext(ctx).Model(&model.AuthProvider{}).
			Where("id = ?", string(row.ID)).Update("enabled", false).Error; err != nil {
			return db.AuthProvider{}, err
		}
		row.Enabled = false
	}
	return toDBAuthProvider(row), nil
}

// UpdateAuthProvider updates the mutable fields and returns the provider.
func (s *Providers) UpdateAuthProvider(ctx context.Context, arg db.UpdateAuthProviderParams) (db.AuthProvider, error) {
	if err := s.db.WithContext(ctx).Model(&model.AuthProvider{}).
		Where("id = ?", UUIDString(arg.ID)).
		Updates(map[string]any{
			"name":       arg.Name,
			"config_enc": arg.ConfigEnc,
			"enabled":    arg.Enabled,
			"updated_at": time.Now().UTC(),
		}).Error; err != nil {
		return db.AuthProvider{}, err
	}
	return s.GetAuthProviderByID(ctx, arg.ID)
}

// RevokeRefreshTokensByAuthProvider revokes the tokens of every linked account.
func (s *Providers) RevokeRefreshTokensByAuthProvider(ctx context.Context, authProviderID db.UUID) error {
	users := s.db.Model(&model.User{}).Select("id").Where("auth_provider_id = ?", UUIDString(authProviderID))
	return s.db.WithContext(ctx).Model(&model.RefreshToken{}).
		Where("revoked = ? AND user_id IN (?)", false, users).
		Update("revoked", true).Error
}

// DeleteAPIKeysByAuthProvider deletes the API keys of every linked account.
func (s *Providers) DeleteAPIKeysByAuthProvider(ctx context.Context, authProviderID db.UUID) error {
	users := s.db.Model(&model.User{}).Select("id").Where("auth_provider_id = ?", UUIDString(authProviderID))
	return s.db.WithContext(ctx).
		Where("user_id IN (?)", users).
		Delete(&model.APIKey{}).Error
}

// SuspendUsersByAuthProvider suspends and detaches every linked account before
// the provider is deleted, so the auth checks still hold.
func (s *Providers) SuspendUsersByAuthProvider(ctx context.Context, authProviderID db.UUID) error {
	now := time.Now().UTC()
	base := map[string]any{
		"status":           "suspended",
		"suspended_at":     now,
		"suspended_reason": "identity_provider_deleted",
		"auth_provider":    "local",
		"auth_provider_id": nil,
		"external_id":      nil,
		"updated_at":       now,
	}
	// Accounts without a password need the sentinel first; both updates detach
	// the rows, so the second only sees the ones with a hash.
	withoutHash := map[string]any{"password_hash": "!"}
	for k, v := range base {
		withoutHash[k] = v
	}
	if err := s.db.WithContext(ctx).Model(&model.User{}).
		Where("auth_provider_id = ? AND password_hash IS NULL", UUIDString(authProviderID)).
		Updates(withoutHash).Error; err != nil {
		return err
	}
	return s.db.WithContext(ctx).Model(&model.User{}).
		Where("auth_provider_id = ? AND password_hash IS NOT NULL", UUIDString(authProviderID)).
		Updates(base).Error
}

// DeleteAuthProvider removes the provider.
func (s *Providers) DeleteAuthProvider(ctx context.Context, id db.UUID) error {
	return s.db.WithContext(ctx).Where("id = ?", UUIDString(id)).Delete(&model.AuthProvider{}).Error
}

// DeleteUserIDPTokensByAuthProvider deletes the sealed IdP tokens of the
// provider.
func (s *Providers) DeleteUserIDPTokensByAuthProvider(ctx context.Context, authProviderID db.UUID) error {
	return s.db.WithContext(ctx).
		Where("auth_provider_id = ?", UUIDString(authProviderID)).
		Delete(&model.UserIDPToken{}).Error
}

func toDBAuthProvider(p model.AuthProvider) db.AuthProvider {
	return db.AuthProvider{
		ID:        PGUUID(string(p.ID)),
		Name:      p.Name,
		Type:      p.Type,
		ConfigEnc: p.ConfigEnc,
		Enabled:   p.Enabled,
		CreatedAt: PGTime(p.CreatedAt),
		UpdatedAt: PGTime(p.UpdatedAt),
	}
}
