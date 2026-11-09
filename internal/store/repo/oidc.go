package repo

import (
	"context"
	"errors"
	"time"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/identity/idptoken"
	"github.com/galileostudio/baleia/internal/store/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// TxIDPToken runs fn in one transaction over the identity-provider token store.
func (r *Repo) TxIDPToken(ctx context.Context, fn func(idptoken.Store) error) error {
	return r.Users.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(New(tx))
	})
}

// CreateOIDCLoginAttempt stores the short-lived state of an OIDC login.
func (r *Repo) CreateOIDCLoginAttempt(ctx context.Context, arg db.CreateOIDCLoginAttemptParams) error {
	row := model.OIDCLoginAttempt{
		State:        model.Text(arg.State),
		ProviderID:   model.UUID(UUIDString(arg.ProviderID)),
		Nonce:        arg.Nonce,
		CodeVerifier: arg.CodeVerifier,
		Redirect:     arg.Redirect,
		ExpiresAt:    arg.ExpiresAt.Time,
		CreatedAt:    time.Now().UTC(),
	}
	return r.Users.db.WithContext(ctx).Create(&row).Error
}

// ConsumeOIDCLoginAttempt deletes the attempt atomically, so a replayed state
// finds nothing.
func (r *Repo) ConsumeOIDCLoginAttempt(ctx context.Context, state string) (db.OidcLoginAttempt, error) {
	var row model.OIDCLoginAttempt
	err := r.Users.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("state = ? AND expires_at > ?", state, time.Now().UTC()).
			First(&row)
		if errors.Is(res.Error, gorm.ErrRecordNotFound) {
			return db.ErrNoRows
		}
		if res.Error != nil {
			return res.Error
		}
		return tx.Where("state = ?", state).Delete(&model.OIDCLoginAttempt{}).Error
	})
	if err != nil {
		return db.OidcLoginAttempt{}, err
	}
	return toDBOIDCLoginAttempt(row), nil
}

// SaveUserIDPTokenOnSignIn keeps a sign-in's tokens only while the account is
// active and its provider enabled. The share locks make a concurrent
// suspension or disable win.
func (r *Repo) SaveUserIDPTokenOnSignIn(ctx context.Context, arg db.SaveUserIDPTokenOnSignInParams) (int64, error) {
	var affected int64
	err := r.Users.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var user model.User
		if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).
			Where("id = ? AND status = ?", UUIDString(arg.UserID), "active").
			First(&user).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		var provider model.AuthProvider
		if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).
			Where("id = ? AND enabled = ?", UUIDString(arg.AuthProviderID), true).
			First(&provider).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		row := model.UserIDPToken{
			UserID:          model.UUID(UUIDString(arg.UserID)),
			AuthProviderID:  model.UUID(UUIDString(arg.AuthProviderID)),
			AccessTokenEnc:  arg.AccessTokenEnc,
			RefreshTokenEnc: arg.RefreshTokenEnc,
			ExpiresAt:       arg.ExpiresAt.Time,
			UpdatedAt:       time.Now().UTC(),
		}
		res := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "user_id"}},
			DoUpdates: clause.AssignmentColumns([]string{"auth_provider_id", "access_token_enc", "refresh_token_enc", "expires_at", "updated_at"}),
		}).Create(&row)
		affected = res.RowsAffected
		return res.Error
	})
	return affected, err
}

// GetUserIDPToken returns the account's sealed tokens or db.ErrNoRows.
func (r *Repo) GetUserIDPToken(ctx context.Context, userID db.UUID) (db.UserIdpToken, error) {
	return r.getUserIDPToken(ctx, userID, false)
}

// GetUserIDPTokenForUpdate locks the account's token row for a refresh.
func (r *Repo) GetUserIDPTokenForUpdate(ctx context.Context, userID db.UUID) (db.UserIdpToken, error) {
	return r.getUserIDPToken(ctx, userID, true)
}

func (r *Repo) getUserIDPToken(ctx context.Context, userID db.UUID, lock bool) (db.UserIdpToken, error) {
	q := r.Users.db.WithContext(ctx)
	if lock {
		q = q.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	var row model.UserIDPToken
	err := q.Where("user_id = ?", UUIDString(userID)).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return db.UserIdpToken{}, db.ErrNoRows
	}
	if err != nil {
		return db.UserIdpToken{}, err
	}
	return toDBUserIDPToken(row), nil
}

// UpdateUserIDPToken writes over the row a refresh locked.
func (r *Repo) UpdateUserIDPToken(ctx context.Context, arg db.UpdateUserIDPTokenParams) error {
	return r.Users.db.WithContext(ctx).Model(&model.UserIDPToken{}).
		Where("user_id = ?", UUIDString(arg.UserID)).
		Updates(map[string]any{
			"access_token_enc":  arg.AccessTokenEnc,
			"refresh_token_enc": arg.RefreshTokenEnc,
			"expires_at":        arg.ExpiresAt.Time,
			"updated_at":        time.Now().UTC(),
		}).Error
}

// GetServiceAccountByClient returns the active service account a client-
// credentials token authenticates as.
func (s *Users) GetServiceAccountByClient(ctx context.Context, arg db.GetServiceAccountByClientParams) (db.User, error) {
	var row model.User
	err := s.db.WithContext(ctx).
		Where("kind = ? AND auth_provider = ? AND auth_provider_id = ? AND external_id = ? AND status = ?",
			"service_account", "oidc", UUIDString(arg.AuthProviderID), arg.ExternalID, "active").
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return db.User{}, db.ErrNoRows
	}
	if err != nil {
		return db.User{}, err
	}
	return toDBUser(row), nil
}

func toDBOIDCLoginAttempt(a model.OIDCLoginAttempt) db.OidcLoginAttempt {
	return db.OidcLoginAttempt{
		State:        string(a.State),
		ProviderID:   PGUUID(string(a.ProviderID)),
		Nonce:        a.Nonce,
		CodeVerifier: a.CodeVerifier,
		Redirect:     a.Redirect,
		ExpiresAt:    PGTime(a.ExpiresAt),
		CreatedAt:    PGTime(a.CreatedAt),
	}
}

func toDBUserIDPToken(t model.UserIDPToken) db.UserIdpToken {
	return db.UserIdpToken{
		UserID:          PGUUID(string(t.UserID)),
		AuthProviderID:  PGUUID(string(t.AuthProviderID)),
		AccessTokenEnc:  t.AccessTokenEnc,
		RefreshTokenEnc: t.RefreshTokenEnc,
		ExpiresAt:       PGTime(t.ExpiresAt),
		UpdatedAt:       PGTime(t.UpdatedAt),
	}
}
