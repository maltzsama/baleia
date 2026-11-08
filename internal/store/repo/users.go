package repo

import (
	"cmp"
	"context"
	"errors"
	"time"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/identity/users"
	"github.com/galileostudio/baleia/internal/store/model"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Users persists accounts, their suspensions and directory runs.
type Users struct{ db *gorm.DB }

// uniqueViolation is PostgreSQL's unique_violation SQLSTATE, returned as a
// pgconn.PgError so the users service classifies it as before.
const uniqueViolation = "23505"

// NewUsers builds the account store.
func NewUsers(db *gorm.DB) *Users { return &Users{db: db} }

// Tx runs fn in one transaction over a Users bound to it.
func (s *Users) Tx(ctx context.Context, fn func(users.TxStore) error) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(&Users{db: tx})
	})
}

// GetUserByID returns one account or pgx.ErrNoRows.
func (s *Users) GetUserByID(ctx context.Context, id pgtype.UUID) (db.User, error) {
	var row model.User
	err := s.db.WithContext(ctx).Where("id = ?", UUIDString(id)).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return db.User{}, pgx.ErrNoRows
	}
	if err != nil {
		return db.User{}, err
	}
	return toDBUser(row), nil
}

// GetUserByEmail returns one account or pgx.ErrNoRows.
func (s *Users) GetUserByEmail(ctx context.Context, email string) (db.User, error) {
	var row model.User
	err := s.db.WithContext(ctx).Where("email = ?", email).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return db.User{}, pgx.ErrNoRows
	}
	if err != nil {
		return db.User{}, err
	}
	return toDBUser(row), nil
}

// ListUsers returns every account, by name.
func (s *Users) ListUsers(ctx context.Context) ([]db.User, error) {
	var rows []model.User
	if err := s.db.WithContext(ctx).Order("name").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]db.User, len(rows))
	for i, row := range rows {
		out[i] = toDBUser(row)
	}
	return out, nil
}

// CreateUser inserts a local or provisioned account.
func (s *Users) CreateUser(ctx context.Context, arg db.CreateUserParams) (db.User, error) {
	now := time.Now().UTC()
	row := model.User{
		ID:             model.UUID(NewUUID()),
		Name:           arg.Name,
		Email:          model.Text(arg.Email),
		PasswordHash:   arg.PasswordHash,
		Role:           model.Text(cmp.Or(arg.Role, "member")),
		AuthProvider:   model.Text(cmp.Or(arg.AuthProvider, "local")),
		AuthProviderID: modelUUIDPtr(arg.AuthProviderID),
		ExternalID:     modelTextPtr(arg.ExternalID),
		Status:         "active",
		Kind:           "human",
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := s.db.WithContext(ctx).Create(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return db.User{}, &pgconn.PgError{Code: uniqueViolation, Message: "duplicate key value violates unique constraint"}
		}
		return db.User{}, err
	}
	return toDBUser(row), nil
}

// UpdateUserRole sets the platform role.
func (s *Users) UpdateUserRole(ctx context.Context, arg db.UpdateUserRoleParams) error {
	return s.db.WithContext(ctx).Model(&model.User{}).
		Where("id = ?", UUIDString(arg.ID)).
		Updates(map[string]any{"role": arg.Role, "updated_at": time.Now().UTC()}).Error
}

// DeleteUser removes the account.
func (s *Users) DeleteUser(ctx context.Context, id pgtype.UUID) error {
	return s.db.WithContext(ctx).Where("id = ?", UUIDString(id)).Delete(&model.User{}).Error
}

// SuspendUser marks the account suspended and returns it.
func (s *Users) SuspendUser(ctx context.Context, arg db.SuspendUserParams) (db.User, error) {
	now := time.Now().UTC()
	if err := s.db.WithContext(ctx).Model(&model.User{}).
		Where("id = ?", UUIDString(arg.ID)).
		Updates(map[string]any{
			"status":           "suspended",
			"suspended_at":     now,
			"suspended_reason": arg.SuspendedReason,
			"updated_at":       now,
		}).Error; err != nil {
		return db.User{}, err
	}
	return s.GetUserByID(ctx, arg.ID)
}

// ReinstateUser lifts any suspension and returns the account.
func (s *Users) ReinstateUser(ctx context.Context, id pgtype.UUID) (db.User, error) {
	if err := s.db.WithContext(ctx).Model(&model.User{}).
		Where("id = ?", UUIDString(id)).
		Updates(map[string]any{
			"status":           "active",
			"suspended_at":     nil,
			"suspended_reason": nil,
			"updated_at":       time.Now().UTC(),
		}).Error; err != nil {
		return db.User{}, err
	}
	return s.GetUserByID(ctx, id)
}

// RevokeUserRefreshTokens revokes every live refresh token of the account.
func (s *Users) RevokeUserRefreshTokens(ctx context.Context, userID pgtype.UUID) error {
	return s.db.WithContext(ctx).Model(&model.RefreshToken{}).
		Where("user_id = ? AND revoked = ?", UUIDString(userID), false).
		Update("revoked", true).Error
}

// DeleteAPIKeysByUser removes the account's API keys.
func (s *Users) DeleteAPIKeysByUser(ctx context.Context, userID pgtype.UUID) error {
	return s.db.WithContext(ctx).Where("user_id = ?", UUIDString(userID)).Delete(&model.APIKey{}).Error
}

// DeleteUserIDPToken removes the account's identity-provider token.
func (s *Users) DeleteUserIDPToken(ctx context.Context, userID pgtype.UUID) error {
	return s.db.WithContext(ctx).Where("user_id = ?", UUIDString(userID)).Delete(&model.UserIDPToken{}).Error
}

// CreatePasswordResetToken stores a single-use reset or invite token.
func (s *Users) CreatePasswordResetToken(ctx context.Context, arg db.CreatePasswordResetTokenParams) error {
	row := model.PasswordResetToken{
		TokenHash: model.Text(arg.TokenHash),
		UserID:    model.UUID(UUIDString(arg.UserID)),
		ExpiresAt: arg.ExpiresAt.Time,
		CreatedAt: time.Now().UTC(),
	}
	if arg.ExpiresAt.Valid {
		row.ExpiresAt = arg.ExpiresAt.Time
	}
	return s.db.WithContext(ctx).Create(&row).Error
}

// ConsumePasswordResetToken marks the token used atomically, so a replayed
// link fails even before its expiry.
func (s *Users) ConsumePasswordResetToken(ctx context.Context, tokenHash string) (db.PasswordResetToken, error) {
	var row model.PasswordResetToken
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("token_hash = ? AND used_at IS NULL AND expires_at > ?", tokenHash, time.Now().UTC()).
			First(&row)
		if errors.Is(res.Error, gorm.ErrRecordNotFound) {
			return pgx.ErrNoRows
		}
		if res.Error != nil {
			return res.Error
		}
		now := time.Now().UTC()
		if err := tx.Model(&model.PasswordResetToken{}).
			Where("token_hash = ?", tokenHash).
			Update("used_at", now).Error; err != nil {
			return err
		}
		row.UsedAt = &now
		return nil
	})
	if err != nil {
		return db.PasswordResetToken{}, err
	}
	return toDBPasswordResetToken(row), nil
}

// UpdateUserPassword replaces the password hash.
func (s *Users) UpdateUserPassword(ctx context.Context, arg db.UpdateUserPasswordParams) error {
	return s.db.WithContext(ctx).Model(&model.User{}).
		Where("id = ?", UUIDString(arg.ID)).
		Updates(map[string]any{"password_hash": arg.PasswordHash, "updated_at": time.Now().UTC()}).Error
}

// ListDirectoryReconcileRuns returns the latest runs, newest first.
func (s *Users) ListDirectoryReconcileRuns(ctx context.Context, arg db.ListDirectoryReconcileRunsParams) ([]db.DirectoryReconcileRun, error) {
	q := s.db.WithContext(ctx).Model(&model.DirectoryReconcileRun{})
	if arg.Column1.Valid {
		q = q.Where("provider_id = ?", UUIDString(arg.Column1))
	}
	var rows []model.DirectoryReconcileRun
	if err := q.Order("started_at DESC").Limit(int(arg.Limit)).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]db.DirectoryReconcileRun, len(rows))
	for i, row := range rows {
		out[i] = toDBDirectoryReconcileRun(row)
	}
	return out, nil
}

func toDBUser(u model.User) db.User {
	return db.User{
		ID:                  PGUUID(string(u.ID)),
		Name:                u.Name,
		Email:               string(u.Email),
		PasswordHash:        u.PasswordHash,
		Role:                string(u.Role),
		AuthProvider:        string(u.AuthProvider),
		AuthProviderID:      uuidPtr(u.AuthProviderID),
		ExternalID:          textPtr(u.ExternalID),
		PersonalWorkspaceID: uuidPtr(u.PersonalWorkspaceID),
		CreatedAt:           PGTime(u.CreatedAt),
		UpdatedAt:           PGTime(u.UpdatedAt),
		Status:              string(u.Status),
		SuspendedAt:         PGTimePtr(u.SuspendedAt),
		SuspendedReason:     u.SuspendedReason,
		DirectorySyncedAt:   PGTimePtr(u.DirectorySyncedAt),
		Locale:              u.Locale,
		Kind:                string(u.Kind),
		HomeWorkspaceID:     uuidPtr(u.HomeWorkspaceID),
		Description:         u.Description,
	}
}

func toDBPasswordResetToken(t model.PasswordResetToken) db.PasswordResetToken {
	return db.PasswordResetToken{
		TokenHash: string(t.TokenHash),
		UserID:    PGUUID(string(t.UserID)),
		ExpiresAt: PGTime(t.ExpiresAt),
		UsedAt:    PGTimePtr(t.UsedAt),
		CreatedAt: PGTime(t.CreatedAt),
	}
}

func toDBDirectoryReconcileRun(r model.DirectoryReconcileRun) db.DirectoryReconcileRun {
	return db.DirectoryReconcileRun{
		ID:         PGUUID(string(r.ID)),
		ProviderID: PGUUID(string(r.ProviderID)),
		StartedAt:  PGTime(r.StartedAt),
		FinishedAt: PGTimePtr(r.FinishedAt),
		Outcome:    r.Outcome,
		Seen:       r.Seen,
		Suspended:  r.Suspended,
		Reinstated: r.Reinstated,
		Error:      r.Error,
		Actions:    []byte(r.Actions),
	}
}

func uuidPtr(u *model.UUID) pgtype.UUID {
	if u == nil {
		return pgtype.UUID{}
	}
	return PGUUID(string(*u))
}

func textPtr(t *model.Text) *string {
	if t == nil {
		return nil
	}
	s := string(*t)
	return &s
}

func modelUUIDPtr(u pgtype.UUID) *model.UUID {
	if !u.Valid {
		return nil
	}
	v := model.UUID(UUIDString(u))
	return &v
}

func modelTextPtr(s *string) *model.Text {
	if s == nil {
		return nil
	}
	v := model.Text(*s)
	return &v
}
