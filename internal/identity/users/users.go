// Package users is the administration of accounts: listing, inviting,
// changing roles, removing, and suspending or reinstating (#258). Callers
// check that the actor is an administrator.
package users

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/mail"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// Reader reads accounts and reconciliation runs; *db.Queries implements it.
type Reader interface {
	GetUserByID(ctx context.Context, id pgtype.UUID) (db.User, error)
	GetUserByEmail(ctx context.Context, email string) (db.User, error)
	ListUsers(ctx context.Context) ([]db.User, error)
	ListDirectoryReconcileRuns(ctx context.Context, arg db.ListDirectoryReconcileRunsParams) ([]db.DirectoryReconcileRun, error)
}

// Writer changes one account; *db.Queries implements it.
type Writer interface {
	UpdateUserRole(ctx context.Context, arg db.UpdateUserRoleParams) error
	DeleteUser(ctx context.Context, id pgtype.UUID) error
	ReinstateUser(ctx context.Context, id pgtype.UUID) (db.User, error)
}

// Store is Reader and Writer.
type Store interface {
	Reader
	Writer
}

// TxStore holds the writes that must commit together; *db.Queries implements it.
type TxStore interface {
	CreateUser(ctx context.Context, arg db.CreateUserParams) (db.User, error)
	CreatePasswordResetToken(ctx context.Context, arg db.CreatePasswordResetTokenParams) error
	SuspendUser(ctx context.Context, arg db.SuspendUserParams) (db.User, error)
	RevokeUserRefreshTokens(ctx context.Context, userID pgtype.UUID) error
	DeleteAPIKeysByUser(ctx context.Context, userID pgtype.UUID) error
}

// Tx runs fn in one transaction over a TxStore.
type Tx func(ctx context.Context, fn func(TxStore) error) error

// Mailer sends the invitation in the recipient's locale.
type Mailer interface {
	Send(ctx context.Context, to, locale string, template mail.Template, data map[string]any) error
}

// Sessions ends every session of an account; *session.Service implements it.
type Sessions interface {
	RevokeAll(ctx context.Context, userID pgtype.UUID)
}

// Config are the invitation settings, resolved by the caller.
type Config struct {
	// PublicURL is where the e-mailed invitation points.
	PublicURL     string
	DefaultLocale string
	PasswordCost  int
}

// InviteTTL is how long an invitation link stays valid.
const InviteTTL = 72 * time.Hour

var (
	// ErrNotFound: no account has this id.
	ErrNotFound = errors.New("users: not found")
	// ErrEmailTaken: an account already uses this e-mail.
	ErrEmailTaken = errors.New("users: e-mail already exists")
)

// Suspension reasons; anything else is recorded as ReasonAdmin, which keeps
// the metric label and the reconciliation vocabulary bounded.
const (
	ReasonAdmin  = "admin"
	ReasonPolicy = "policy"
)

// Service administers accounts.
type Service struct {
	store    Store
	tx       Tx
	mailer   Mailer
	sessions Sessions
	cfg      Config
}

// New builds the service.
func New(store Store, tx Tx, mailer Mailer, sessions Sessions, cfg Config) *Service {
	return &Service{store: store, tx: tx, mailer: mailer, sessions: sessions, cfg: cfg}
}

// List returns every account.
func (s *Service) List(ctx context.Context) ([]db.User, error) {
	return s.store.ListUsers(ctx)
}

// Invite creates a local account with an unusable random password and
// e-mails it a link to set one. The account and the link are one write: an
// account without a link could neither sign in nor be invited again (#376).
// The e-mail goes out after the commit; a failed send is logged, not returned.
func (s *Service) Invite(ctx context.Context, email, role string) (db.User, error) {
	if _, err := s.store.GetUserByEmail(ctx, email); err == nil {
		return db.User{}, ErrEmailTaken
	}
	hash, err := auth.HashPasswordWithCost(newToken(), s.cfg.PasswordCost)
	if err != nil {
		return db.User{}, err
	}
	token := newToken()

	var user db.User
	err = s.tx(ctx, func(q TxStore) error {
		var err error
		user, err = q.CreateUser(ctx, db.CreateUserParams{
			Name:         email,
			Email:        email,
			Role:         role,
			AuthProvider: "local",
			PasswordHash: &hash,
		})
		if err != nil {
			return fmt.Errorf("create user: %w", err)
		}
		// The invite link carries the raw token; only its hash is stored (#51).
		if err := q.CreatePasswordResetToken(ctx, db.CreatePasswordResetTokenParams{
			TokenHash: auth.HashToken(token),
			UserID:    user.ID,
			ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(InviteTTL), Valid: true},
		}); err != nil {
			return fmt.Errorf("create invite token: %w", err)
		}
		return nil
	})
	if err != nil {
		return db.User{}, err
	}

	inviteURL := s.cfg.PublicURL + "/accept-invite?token=" + token
	locale := mail.LocaleOrDefault(user.Locale, s.cfg.DefaultLocale)
	if err := s.mailer.Send(ctx, user.Email, locale, mail.TemplateInvite, map[string]any{
		"Name":      user.Name,
		"InviteURL": inviteURL,
	}); err != nil {
		// The link carries the invitation token: never log it.
		slog.Warn("failed to send invite email", "err", err, "user_id", user.ID.String())
	}
	return user, nil
}

// ChangeRole sets the platform role and ends the account's sessions, so the
// new role applies at the next sign-in (#51). It returns the account before
// and after.
func (s *Service) ChangeRole(ctx context.Context, id pgtype.UUID, role string) (before, after db.User, err error) {
	if before, err = s.get(ctx, id); err != nil {
		return db.User{}, db.User{}, err
	}
	if err := s.store.UpdateUserRole(ctx, db.UpdateUserRoleParams{ID: id, Role: role}); err != nil {
		return db.User{}, db.User{}, err
	}
	if after, err = s.store.GetUserByID(ctx, id); err != nil {
		return db.User{}, db.User{}, err
	}
	s.sessions.RevokeAll(ctx, id)
	return before, after, nil
}

// Remove deletes the account. A delete the database refuses (the account is
// still referenced) is returned unwrapped for the caller to classify.
func (s *Service) Remove(ctx context.Context, id pgtype.UUID) error {
	if _, err := s.get(ctx, id); err != nil {
		return err
	}
	if err := s.store.DeleteUser(ctx, id); err != nil {
		return err
	}
	// The foreign key already cascades a delete; revoking explicitly also
	// covers a future soft disable that keeps the row (#51).
	s.sessions.RevokeAll(ctx, id)
	return nil
}

// Suspend suspends the account and, in the same transaction, ends its
// sessions and deletes its API keys. It returns the account before and after
// and the recorded reason.
func (s *Service) Suspend(ctx context.Context, id pgtype.UUID, reason string) (before, after db.User, recorded string, err error) {
	if before, err = s.get(ctx, id); err != nil {
		return db.User{}, db.User{}, "", err
	}
	recorded = ReasonAdmin
	if reason == ReasonPolicy {
		recorded = ReasonPolicy
	}
	err = s.tx(ctx, func(q TxStore) error {
		var err error
		if after, err = q.SuspendUser(ctx, db.SuspendUserParams{ID: id, SuspendedReason: &recorded}); err != nil {
			return err
		}
		if err := q.RevokeUserRefreshTokens(ctx, id); err != nil {
			return err
		}
		return q.DeleteAPIKeysByUser(ctx, id)
	})
	if err != nil {
		return db.User{}, db.User{}, "", err
	}
	return before, after, recorded, nil
}

// Reinstate lifts any suspension, including a directory one (#258). It
// returns the account before and after.
func (s *Service) Reinstate(ctx context.Context, id pgtype.UUID) (before, after db.User, err error) {
	if before, err = s.get(ctx, id); err != nil {
		return db.User{}, db.User{}, err
	}
	if after, err = s.store.ReinstateUser(ctx, id); err != nil {
		return db.User{}, db.User{}, err
	}
	return before, after, nil
}

// ReconcileRuns lists the latest directory reconciliation runs, of one
// provider when providerID is valid.
func (s *Service) ReconcileRuns(ctx context.Context, providerID pgtype.UUID, limit int32) ([]db.DirectoryReconcileRun, error) {
	return s.store.ListDirectoryReconcileRuns(ctx, db.ListDirectoryReconcileRunsParams{Column1: providerID, Limit: limit})
}

func (s *Service) get(ctx context.Context, id pgtype.UUID) (db.User, error) {
	user, err := s.store.GetUserByID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return db.User{}, ErrNotFound
	}
	if err != nil {
		return db.User{}, err
	}
	return user, nil
}

// newToken is 128 random bits, URL-safe and unpadded.
func newToken() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}
