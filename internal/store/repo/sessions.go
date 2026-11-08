package repo

import (
	"context"
	"errors"
	"time"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/store/model"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
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

// ConsumeRefreshToken rotates the token atomically: of two concurrent
// refreshes with the same token, exactly one updates a row.
func (s *Sessions) ConsumeRefreshToken(ctx context.Context, tokenHash string) (db.ConsumeRefreshTokenRow, error) {
	var row model.RefreshToken
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("token_hash = ? AND revoked = ? AND expires_at > ?", tokenHash, false, time.Now().UTC()).
			First(&row)
		if errors.Is(res.Error, gorm.ErrRecordNotFound) {
			return pgx.ErrNoRows
		}
		if res.Error != nil {
			return res.Error
		}
		return tx.Model(&model.RefreshToken{}).
			Where("id = ?", string(row.ID)).
			Update("revoked", true).Error
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

// GetRefreshTokenByHashAny returns the row even when revoked, for reuse
// detection.
func (s *Sessions) GetRefreshTokenByHashAny(ctx context.Context, tokenHash string) (db.RefreshToken, error) {
	var row model.RefreshToken
	err := s.db.WithContext(ctx).Where("token_hash = ?", tokenHash).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return db.RefreshToken{}, pgx.ErrNoRows
	}
	if err != nil {
		return db.RefreshToken{}, err
	}
	return toDBRefreshToken(row), nil
}

// RevokeRefreshFamily revokes every live token in the family.
func (s *Sessions) RevokeRefreshFamily(ctx context.Context, familyID pgtype.UUID) error {
	return s.db.WithContext(ctx).Model(&model.RefreshToken{}).
		Where("family_id = ? AND revoked = ?", UUIDString(familyID), false).
		Update("revoked", true).Error
}

// RevokeUserRefreshTokens revokes every live token of the account.
func (s *Sessions) RevokeUserRefreshTokens(ctx context.Context, userID pgtype.UUID) error {
	return s.db.WithContext(ctx).Model(&model.RefreshToken{}).
		Where("user_id = ? AND revoked = ?", UUIDString(userID), false).
		Update("revoked", true).Error
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
