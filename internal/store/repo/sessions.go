package repo

import (
	"context"
	"errors"
	"time"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/store/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Sessions persists refresh tokens and their rotation families.
type Sessions struct{ db *gorm.DB }

// NewSessions builds the refresh-token store.
func NewSessions(db *gorm.DB) *Sessions { return &Sessions{db: db} }

// CreateRefreshToken inserts one rotation member.
func (s *Sessions) CreateRefreshToken(ctx context.Context, arg db.CreateRefreshTokenParams) (db.RefreshToken, error) {
	row := model.RefreshToken{
		ID:        model.UUID(NewUUID()),
		UserID:    model.UUID(UUIDString(arg.UserID)),
		TokenHash: model.Text(arg.TokenHash),
		ExpiresAt: arg.ExpiresAt.Time,
		FamilyID:  model.UUID(UUIDString(arg.FamilyID)),
		CreatedAt: time.Now().UTC(),
	}
	if err := s.db.WithContext(ctx).Create(&row).Error; err != nil {
		return db.RefreshToken{}, err
	}
	return toDBRefreshToken(row), nil
}

// RotateRefreshToken revokes the live token and inserts its successor in the
// same family in one transaction: of two concurrent refreshes with the same
// token exactly one succeeds, and the family never looks dead in between, so an
// access token bound to it is not refused mid-rotation.
func (s *Sessions) RotateRefreshToken(ctx context.Context, arg db.RotateRefreshTokenParams) (db.ConsumeRefreshTokenRow, error) {
	var row model.RefreshToken
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// The owner is found first and locked before any token row, the same
		// order every revocation takes, so a revoke can never run between the
		// old token's revocation and its successor's insert.
		var owner model.RefreshToken
		res := tx.Select("user_id").Where("token_hash = ?", arg.TokenHash).First(&owner)
		if errors.Is(res.Error, gorm.ErrRecordNotFound) {
			return db.ErrNoRows
		}
		if res.Error != nil {
			return res.Error
		}
		if _, err := lockUser(tx, string(owner.UserID)); err != nil {
			return err
		}
		res = tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("token_hash = ? AND revoked = ? AND expires_at > ?", arg.TokenHash, false, time.Now().UTC()).
			First(&row)
		if errors.Is(res.Error, gorm.ErrRecordNotFound) {
			return db.ErrNoRows
		}
		if res.Error != nil {
			return res.Error
		}
		if err := tx.Model(&model.RefreshToken{}).
			Where("id = ?", string(row.ID)).
			Update("revoked", true).Error; err != nil {
			return err
		}
		return tx.Create(&model.RefreshToken{
			ID:        model.UUID(NewUUID()),
			UserID:    row.UserID,
			TokenHash: model.Text(arg.NewTokenHash),
			ExpiresAt: arg.NewExpiresAt.Time,
			FamilyID:  row.FamilyID,
			CreatedAt: time.Now().UTC(),
		}).Error
	})
	if err != nil {
		return db.ConsumeRefreshTokenRow{}, err
	}
	return db.ConsumeRefreshTokenRow{
		ID:       PGUUID(string(row.ID)),
		UserID:   PGUUID(string(row.UserID)),
		FamilyID: PGUUID(string(row.FamilyID)),
	}, nil
}

// LiveRefreshFamily reports whether the family has a token that is neither
// revoked nor expired.
func (s *Sessions) LiveRefreshFamily(ctx context.Context, familyID db.UUID) (bool, error) {
	var n int64
	err := s.db.WithContext(ctx).Model(&model.RefreshToken{}).
		Where("family_id = ? AND revoked = ? AND expires_at > ?", UUIDString(familyID), false, time.Now().UTC()).
		Limit(1).Count(&n).Error
	return n > 0, err
}

// GetRefreshTokenByHashAny returns the row even when revoked, for reuse
// detection.
func (s *Sessions) GetRefreshTokenByHashAny(ctx context.Context, tokenHash string) (db.RefreshToken, error) {
	var row model.RefreshToken
	err := s.db.WithContext(ctx).Where("token_hash = ?", tokenHash).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return db.RefreshToken{}, db.ErrNoRows
	}
	if err != nil {
		return db.RefreshToken{}, err
	}
	return toDBRefreshToken(row), nil
}

// lockUser takes the account's row lock for the rest of the transaction. It
// reports false when the account does not exist.
func lockUser(tx *gorm.DB, userID string) (bool, error) {
	var user model.User
	res := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id").Where("id = ?", userID).First(&user)
	if errors.Is(res.Error, gorm.ErrRecordNotFound) {
		return false, nil
	}
	return res.Error == nil, res.Error
}

// RevokeRefreshFamily revokes every live token in the family. It serializes
// with a rotation on the owner's row: without that, under Read Committed a
// revoke that waits on the rotating token skips it once it commits and never
// sees the successor, which would stay live.
func (s *Sessions) RevokeRefreshFamily(ctx context.Context, familyID db.UUID) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var owner model.RefreshToken
		res := tx.Select("user_id").Where("family_id = ?", UUIDString(familyID)).First(&owner)
		if errors.Is(res.Error, gorm.ErrRecordNotFound) {
			return nil
		}
		if res.Error != nil {
			return res.Error
		}
		if ok, err := lockUser(tx, string(owner.UserID)); err != nil || !ok {
			return err
		}
		return tx.Model(&model.RefreshToken{}).
			Where("family_id = ? AND revoked = ?", UUIDString(familyID), false).
			Update("revoked", true).Error
	})
}

// RevokeUserRefreshTokens revokes every live token of the account, serialized
// with a rotation like RevokeRefreshFamily.
func (s *Sessions) RevokeUserRefreshTokens(ctx context.Context, userID db.UUID) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if ok, err := lockUser(tx, UUIDString(userID)); err != nil || !ok {
			return err
		}
		return tx.Model(&model.RefreshToken{}).
			Where("user_id = ? AND revoked = ?", UUIDString(userID), false).
			Update("revoked", true).Error
	})
}

func toDBRefreshToken(t model.RefreshToken) db.RefreshToken {
	return db.RefreshToken{
		ID:        PGUUID(string(t.ID)),
		UserID:    PGUUID(string(t.UserID)),
		TokenHash: string(t.TokenHash),
		ExpiresAt: PGTime(t.ExpiresAt),
		Revoked:   t.Revoked,
		CreatedAt: PGTime(t.CreatedAt),
		FamilyID:  PGUUID(string(t.FamilyID)),
	}
}
