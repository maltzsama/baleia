// Package session issues and rotates sign-in sessions: a short-lived access
// token and a refresh token whose rotations share a family (#51). Replaying a
// rotated refresh token revokes its whole family.
package session

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/google/uuid"
)

// Tokens persists refresh tokens; *repo.Repo implements it.
type Tokens interface {
	CreateRefreshToken(ctx context.Context, arg db.CreateRefreshTokenParams) (db.RefreshToken, error)
	RotateRefreshToken(ctx context.Context, arg db.RotateRefreshTokenParams) (db.ConsumeRefreshTokenRow, error)
	GetRefreshTokenByHashAny(ctx context.Context, tokenHash string) (db.RefreshToken, error)
	RevokeRefreshFamily(ctx context.Context, familyID db.UUID) error
	RevokeUserRefreshTokens(ctx context.Context, userID db.UUID) error
}

// Users loads the account a refresh token belongs to; *repo.Repo implements it.
type Users interface {
	GetUserByID(ctx context.Context, id db.UUID) (db.User, error)
}

// Config are the token settings, resolved by the caller.
type Config struct {
	JWTSecret  string
	AccessTTL  time.Duration
	RefreshTTL time.Duration
	// Logger receives the logs; nil discards them (#379).
	Logger *slog.Logger
}

var (
	// ErrInvalid: the refresh token is unknown, expired or already used.
	ErrInvalid = errors.New("session: invalid or expired refresh token")
	// ErrReused: a rotated refresh token was replayed; its family is revoked.
	ErrReused = errors.New("session: refresh token reused")
	// ErrReuseNotContained: a replay was detected but revoking the family
	// failed, so it may still be usable. Never report it as a plain 401 (#370).
	ErrReuseNotContained = errors.New("session: reused refresh token family not revoked")
	// ErrUserNotFound: the token's account no longer exists.
	ErrUserNotFound = errors.New("session: user not found")
	// ErrSuspended: the account is suspended; all its sessions were revoked (#283).
	ErrSuspended = errors.New("session: account suspended")
)

// Pair is an access token and the refresh token that renews it.
type Pair struct {
	AccessToken  string
	RefreshToken string
	// ExpiresAt is when the access token expires.
	ExpiresAt time.Time
}

// Service issues, rotates and ends sessions.
type Service struct {
	logger *slog.Logger
	tokens Tokens
	users  Users
	cfg    Config
}

// New builds the service.
func New(tokens Tokens, users Users, cfg Config) *Service {
	return &Service{tokens: tokens, users: users, cfg: cfg, logger: cmp.Or(cfg.Logger, slog.New(slog.DiscardHandler))}
}

// Issue starts a session for user in a new refresh-token family.
func (s *Service) Issue(ctx context.Context, user db.User) (Pair, error) {
	return s.issue(ctx, user, db.UUID{Bytes: uuid.New(), Valid: true})
}

// IssueRefresh starts a session with only a refresh token, for a browser
// sign-in that redirects and renews its access token afterwards (OIDC).
func (s *Service) IssueRefresh(ctx context.Context, user db.User) (string, error) {
	refresh, err := s.newRefresh(ctx, user, db.UUID{Bytes: uuid.New(), Valid: true})
	if err != nil {
		return "", err
	}
	return refresh, nil
}

// Rotate consumes a refresh token and issues the next pair in its family.
// Exactly one of two concurrent rotations with the same token succeeds (#51).
func (s *Service) Rotate(ctx context.Context, refreshToken string) (Pair, db.User, error) {
	hash := auth.HashRefreshToken(refreshToken)
	next, nextHash, err := auth.GenerateRefreshToken()
	if err != nil {
		return Pair{}, db.User{}, err
	}
	// The successor is created with the revocation, in one transaction, so the
	// family stays live for the access tokens bound to it (#1030).
	rt, err := s.tokens.RotateRefreshToken(ctx, db.RotateRefreshTokenParams{
		TokenHash:    hash,
		NewTokenHash: nextHash,
		NewExpiresAt: db.Timestamptz{Time: time.Now().Add(s.cfg.RefreshTTL), Valid: true},
	})
	if err != nil && !errors.Is(err, db.ErrNoRows) {
		return Pair{}, db.User{}, err
	}
	if err != nil {
		// No live row: a revoked one with this hash means a rotated token was
		// replayed, so the whole family goes (#51).
		old, lookupErr := s.tokens.GetRefreshTokenByHashAny(ctx, hash)
		if lookupErr != nil && !errors.Is(lookupErr, db.ErrNoRows) {
			return Pair{}, db.User{}, fmt.Errorf("look up refresh token: %w", lookupErr)
		}
		if lookupErr == nil && old.Revoked {
			// A client disconnect must not cancel the revoke halfway (#370).
			if revokeErr := s.tokens.RevokeRefreshFamily(context.WithoutCancel(ctx), old.FamilyID); revokeErr != nil {
				return Pair{}, db.User{}, fmt.Errorf("%w: %w", ErrReuseNotContained, revokeErr)
			}
			return Pair{}, db.User{}, ErrReused
		}
		return Pair{}, db.User{}, ErrInvalid
	}

	user, err := s.users.GetUserByID(ctx, rt.UserID)
	if errors.Is(err, db.ErrNoRows) {
		return Pair{}, db.User{}, ErrUserNotFound
	}
	if err != nil {
		return Pair{}, db.User{}, err
	}
	// A suspension ends every session and blocks a refresh even if a token
	// survived (#283).
	if err := auth.RequireActive(user.Status); err != nil {
		s.RevokeAll(ctx, user.ID)
		return Pair{}, user, ErrSuspended
	}

	access, err := auth.NewAccessTokenWithTTL(user.ID.String(), user.Role, rt.FamilyID.String(), s.cfg.JWTSecret, s.cfg.AccessTTL)
	if err != nil {
		return Pair{}, db.User{}, err
	}
	return Pair{AccessToken: access, RefreshToken: next, ExpiresAt: time.Now().Add(s.cfg.AccessTTL)}, user, nil
}

// End revokes the whole family of refreshToken. found is false, with no
// error, when the token is unknown; a store failure is an error, so the
// caller never reports a logout that did not happen.
func (s *Service) End(ctx context.Context, refreshToken string) (userID db.UUID, found bool, err error) {
	rt, err := s.tokens.GetRefreshTokenByHashAny(ctx, auth.HashRefreshToken(refreshToken))
	if errors.Is(err, db.ErrNoRows) {
		return db.UUID{}, false, nil
	}
	if err != nil {
		return db.UUID{}, false, fmt.Errorf("look up refresh token: %w", err)
	}
	// A client disconnect must not cancel the revoke halfway (#370).
	if err := s.tokens.RevokeRefreshFamily(context.WithoutCancel(ctx), rt.FamilyID); err != nil {
		return rt.UserID, true, fmt.Errorf("revoke refresh family: %w", err)
	}
	return rt.UserID, true, nil
}

// RevokeAll ends every session of a user after a security-relevant change; the
// access tokens bound to those sessions are refused on their next request
// (#1030). A failure is
// logged, not returned: the triggering change already succeeded.
func (s *Service) RevokeAll(ctx context.Context, userID db.UUID) {
	if err := s.tokens.RevokeUserRefreshTokens(ctx, userID); err != nil {
		s.logger.ErrorContext(ctx, "RevokeUserRefreshTokens failed", "user_id", userID.String(), "err", err)
	}
}

func (s *Service) issue(ctx context.Context, user db.User, family db.UUID) (Pair, error) {
	access, err := auth.NewAccessTokenWithTTL(user.ID.String(), user.Role, family.String(), s.cfg.JWTSecret, s.cfg.AccessTTL)
	if err != nil {
		return Pair{}, err
	}
	refresh, err := s.newRefresh(ctx, user, family)
	if err != nil {
		return Pair{}, err
	}
	return Pair{AccessToken: access, RefreshToken: refresh, ExpiresAt: time.Now().Add(s.cfg.AccessTTL)}, nil
}

func (s *Service) newRefresh(ctx context.Context, user db.User, family db.UUID) (string, error) {
	refresh, hash, err := auth.GenerateRefreshToken()
	if err != nil {
		return "", err
	}
	if _, err := s.tokens.CreateRefreshToken(ctx, db.CreateRefreshTokenParams{
		UserID:    user.ID,
		TokenHash: hash,
		ExpiresAt: db.Timestamptz{Time: time.Now().Add(s.cfg.RefreshTTL), Valid: true},
		FamilyID:  family,
	}); err != nil {
		return "", err
	}
	return refresh, nil
}
