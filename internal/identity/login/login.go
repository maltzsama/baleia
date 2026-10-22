// Package login verifies sign-in credentials: a local password or a directory
// (LDAP) bind. A failure never reveals whether the account exists, is SSO-only
// or, for a directory account, is suspended (#51, #284).
package login

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/crypto"
	"github.com/galileostudio/baleia/internal/db"
	identityldap "github.com/galileostudio/baleia/internal/identity/ldap"
	"github.com/galileostudio/baleia/internal/identity/provision"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"golang.org/x/crypto/bcrypt"
)

// Store reads accounts and providers and applies a directory login's role
// and teams; *db.Queries implements it.
type Store interface {
	GetUserByEmail(ctx context.Context, email string) (db.User, error)
	GetAuthProviderByID(ctx context.Context, id pgtype.UUID) (db.AuthProvider, error)
	GetUserByExternalID(ctx context.Context, arg db.GetUserByExternalIDParams) (db.User, error)
	UpdateUserRole(ctx context.Context, arg db.UpdateUserRoleParams) error
	provision.TeamMembers
}

// Directory verifies a user's credentials against an LDAP directory.
type Directory interface {
	Authenticate(ctx context.Context, cfg identityldap.Config, username, password string) (*identityldap.User, error)
}

// LDAP binds to the configured server.
type LDAP struct{}

// Authenticate binds as the user and maps their groups.
func (LDAP) Authenticate(ctx context.Context, cfg identityldap.Config, username, password string) (*identityldap.User, error) {
	return identityldap.Authenticate(ctx, cfg, username, password)
}

var (
	// ErrInvalidCredentials: unknown account, SSO-only account, wrong
	// password, or a provider that cannot be used. Indistinguishable on purpose.
	ErrInvalidCredentials = errors.New("login: invalid credentials")
	// ErrSuspended: the local password is correct but the account is
	// suspended, so revealing it is safe (#283).
	ErrSuspended = errors.New("login: account suspended")
)

// Service verifies credentials.
type Service struct {
	store     Store
	directory Directory
	keyring   *crypto.Keyring
	cost      int

	dummyOnce sync.Once
	dummyHash string
}

// New builds the service. passwordCost is the bcrypt cost of the dummy hash
// compared on the unknown-account paths; keyring opens LDAP configurations.
func New(store Store, directory Directory, keyring *crypto.Keyring, passwordCost int) *Service {
	return &Service{store: store, directory: directory, keyring: keyring, cost: passwordCost}
}

// Password verifies a local account's e-mail and password.
func (s *Service) Password(ctx context.Context, email, password string) (db.User, error) {
	user, err := s.store.GetUserByEmail(ctx, email)
	if errors.Is(err, pgx.ErrNoRows) {
		// Unknown e-mail: compare against a dummy hash anyway so the response
		// time does not reveal whether the account exists (#51).
		_ = auth.CheckPassword(s.dummyPasswordHash(), password)
		return db.User{}, ErrInvalidCredentials
	}
	if err != nil {
		return db.User{}, fmt.Errorf("look up account: %w", err)
	}
	// An SSO-only account and a wrong password are indistinguishable (#51).
	if user.AuthProvider != "local" || user.PasswordHash == nil {
		_ = auth.CheckPassword(s.dummyPasswordHash(), password)
		return db.User{}, ErrInvalidCredentials
	}
	if !auth.CheckPassword(*user.PasswordHash, password) {
		return db.User{}, ErrInvalidCredentials
	}
	if err := auth.RequireActive(user.Status); err != nil {
		return db.User{}, ErrSuspended
	}
	return user, nil
}

// Directory verifies a directory login through the enabled LDAP provider
// providerID, then syncs the account's role and teams from the directory.
func (s *Service) Directory(ctx context.Context, providerID pgtype.UUID, username, password string) (db.User, error) {
	if username == "" || password == "" {
		return db.User{}, ErrInvalidCredentials
	}
	ap, err := s.store.GetAuthProviderByID(ctx, providerID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return db.User{}, fmt.Errorf("look up provider: %w", err)
	}
	if err != nil || ap.Type != "ldap" || !ap.Enabled {
		return db.User{}, ErrInvalidCredentials
	}
	cfg, err := identityldap.OpenConfig(s.keyring, ap.ID.String(), ap.ConfigEnc)
	if err != nil {
		// Fail closed: an unreadable config never falls back to another method.
		return db.User{}, ErrInvalidCredentials
	}
	result, err := s.directory.Authenticate(ctx, cfg, username, password)
	if err != nil {
		return db.User{}, ErrInvalidCredentials
	}

	user, err := s.store.GetUserByExternalID(ctx, db.GetUserByExternalIDParams{
		AuthProviderID: ap.ID,
		ExternalID:     &result.ExternalID,
	})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return db.User{}, fmt.Errorf("look up directory account: %w", err)
	}
	if err != nil || user.AuthProvider != "ldap" {
		return db.User{}, ErrInvalidCredentials
	}
	// A suspended directory account is indistinguishable from bad credentials
	// (#284): never reveal the suspension to an unauthenticated caller.
	if err := auth.RequireActive(user.Status); err != nil {
		return db.User{}, ErrInvalidCredentials
	}
	if result.Role != user.Role {
		if err := s.store.UpdateUserRole(ctx, db.UpdateUserRoleParams{ID: user.ID, Role: result.Role}); err != nil {
			return db.User{}, err
		}
		user.Role = result.Role
	}
	provision.ApplyTeams(ctx, s.store, user.ID, result.Teams)
	return user, nil
}

// dummyPasswordHash is a bcrypt hash at the configured cost, generated once
// and only ever compared against, never accepted (#51).
func (s *Service) dummyPasswordHash() string {
	s.dummyOnce.Do(func() {
		cost := bcrypt.DefaultCost
		if s.cost >= bcrypt.MinCost {
			cost = s.cost
		}
		s.dummyHash, _ = auth.HashPasswordWithCost("baleia-dummy-password", cost)
	})
	return s.dummyHash
}
