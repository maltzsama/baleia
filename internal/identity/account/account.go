// Package account is the signed-in user's own account: reading it, editing
// the profile and changing the password.
package account

import (
	"context"
	"errors"

	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/jackc/pgx/v5/pgtype"
)

// Store reads and updates accounts; *db.Queries implements it.
type Store interface {
	GetUserByID(ctx context.Context, id pgtype.UUID) (db.User, error)
	UpdateUser(ctx context.Context, arg db.UpdateUserParams) (db.User, error)
	UpdateUserPassword(ctx context.Context, arg db.UpdateUserPasswordParams) error
	PersonalWorkspaceID(ctx context.Context, ownerUserID pgtype.UUID) (pgtype.UUID, error)
}

// Sessions ends every session of an account; *session.Service implements it.
type Sessions interface {
	RevokeAll(ctx context.Context, userID pgtype.UUID)
}

// Config are the password settings, resolved by the caller.
type Config struct {
	PasswordCost      int
	MinPasswordLength int
}

var (
	// ErrNotFound: the account no longer exists.
	ErrNotFound = errors.New("account: not found")
	// ErrWrongPassword: the current password does not match, or the account
	// has no local password.
	ErrWrongPassword = errors.New("account: current password is incorrect")
	// ErrPasswordTooShort: the new password is below the minimum length.
	ErrPasswordTooShort = errors.New("account: password too short")
)

// Profile is a partial profile change; nil fields keep their value.
type Profile struct {
	Name   *string
	Email  *string
	Locale *string
}

// Service manages the signed-in user's account.
type Service struct {
	store    Store
	sessions Sessions
	cfg      Config
}

// New builds the service.
func New(store Store, sessions Sessions, cfg Config) *Service {
	return &Service{store: store, sessions: sessions, cfg: cfg}
}

// Get returns the account.
func (s *Service) Get(ctx context.Context, id pgtype.UUID) (db.User, error) {
	user, err := s.store.GetUserByID(ctx, id)
	if err != nil {
		return db.User{}, ErrNotFound
	}
	return user, nil
}

// PersonalWorkspace returns the account's personal workspace, if it has one.
func (s *Service) PersonalWorkspace(ctx context.Context, id pgtype.UUID) (pgtype.UUID, bool) {
	ws, err := s.store.PersonalWorkspaceID(ctx, id)
	return ws, err == nil
}

// UpdateProfile applies the non-nil fields of change. The caller validates
// the locale against the supported ones.
func (s *Service) UpdateProfile(ctx context.Context, id pgtype.UUID, change Profile) (db.User, error) {
	current, err := s.Get(ctx, id)
	if err != nil {
		return db.User{}, err
	}
	params := db.UpdateUserParams{ID: id, Name: current.Name, Email: current.Email, Locale: current.Locale}
	if change.Name != nil {
		params.Name = *change.Name
	}
	if change.Email != nil {
		params.Email = *change.Email
	}
	if change.Locale != nil {
		params.Locale = change.Locale
	}
	return s.store.UpdateUser(ctx, params)
}

// ChangePassword replaces the password after checking the current one, then
// ends every session of the account (#51).
func (s *Service) ChangePassword(ctx context.Context, id pgtype.UUID, current, next string) error {
	user, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	if user.PasswordHash == nil || !auth.CheckPassword(*user.PasswordHash, current) {
		return ErrWrongPassword
	}
	if len(next) < s.cfg.MinPasswordLength {
		return ErrPasswordTooShort
	}
	hash, err := auth.HashPasswordWithCost(next, s.cfg.PasswordCost)
	if err != nil {
		return err
	}
	if err := s.store.UpdateUserPassword(ctx, db.UpdateUserPasswordParams{ID: id, PasswordHash: &hash}); err != nil {
		return err
	}
	s.sessions.RevokeAll(ctx, id)
	return nil
}
