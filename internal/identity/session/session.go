// Package session issues and rotates sign-in sessions: a short-lived access
// token and a refresh token whose rotations share a family (#51). Replaying a
// rotated refresh token revokes its whole family.
package session

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// Tokens persists refresh tokens; *db.Queries implements it.
type Tokens interface {
	CreateRefreshToken(ctx context.Context, arg db.CreateRefreshTokenParams) (db.RefreshToken, error)
	ConsumeRefreshToken(ctx context.Context, tokenHash string) (db.ConsumeRefreshTokenRow, error)
	GetRefreshTokenByHashAny(ctx context.Context, tokenHash string) (db.RefreshToken, error)
	RevokeRefreshFamily(ctx context.Context, familyID pgtype.UUID) error
	RevokeUserRefreshTokens(ctx context.Context, userID pgtype.UUID) error
}

// Users loads the account a refresh token belongs to; *db.Queries implements it.
type Users interface {
	GetUserByID(ctx context.Context, id pgtype.UUID) (db.User, error)
}

// Config are the token settings, resolved by the caller.
type Config struct {
	JWTSecret  string
	AccessTTL  time.Duration
	RefreshTTL time.Duration
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
	tokens Tokens
	users  Users
	cfg    Config
}

// New builds the service.
func New(tokens Tokens, users Users, cfg Config) *Service {
	return &Service{tokens: tokens, users: users, cfg: cfg}
}

// Issue starts a session for user in a new refresh-token family.
func (s *Service) Issue(ctx context.Context, user db.User) (Pair, error) {
	return s.issue(ctx, user, pgtype.UUID{Bytes: uuid.New(), Valid: true})
}

// IssueRefresh starts a session with only a refresh token, for a browser
// sign-in that redirects and renews its access token afterwards (OIDC).
func (s *Service) IssueRefresh(ctx context.Context, user db.User) (string, error) {
	refresh, err := s.newRefresh(ctx, user, pgtype.UUID{Bytes: uuid.New(), Valid: true})
	if err != nil {
		return "", err
	}
	return refresh, nil
}

// Rotate consumes a refresh token and issues the next pair in its family.
// Exactly one of two concurrent rotations with the same token succeeds (#51).
func (s *Service) Rotate(ctx context.Context, refreshToken string) (Pair, db.User, error) {
	hash := auth.HashRefreshToken(refreshToken)
	rt, err := s.tokens.ConsumeRefreshToken(ctx, hash)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return Pair{}, db.User{}, err
	}
	if err != nil {
		// No live row: a revoked one with this hash means a rotated token was
		// replayed, so the whole family goes (#51).
		if old, lookupErr := s.tokens.GetRefreshTokenByHashAny(ctx, hash); lookupErr == nil && old.Revoked {
			// A client disconnect must not cancel the revoke halfway (#370).
			if revokeErr := s.tokens.RevokeRefreshFamily(context.WithoutCancel(ctx), old.FamilyID); revokeErr != nil {
				return Pair{}, db.User{}, fmt.Errorf("%w: %w", ErrReuseNotContained, revokeErr)
			}
			return Pair{}, db.User{}, ErrReused
		}
		return Pair{}, db.User{}, ErrInvalid
	}

	user, err := s.users.GetUserByID(ctx, rt.UserID)
	if errors.Is(err, pgx.ErrNoRows) {
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

	pair, err := s.issue(ctx, user, rt.FamilyID)
	if err != nil {
		return Pair{}, db.User{}, err
	}
	return pair, user, nil
}

// End revokes the whole family of refreshToken. found is false, with no
// error, when the token is unknown; a store failure is an error, so the
// caller never reports a logout that did not happen.
func (s *Service) End(ctx context.Context, refreshToken string) (userID pgtype.UUID, found bool, err error) {
	rt, err := s.tokens.GetRefreshTokenByHashAny(ctx, auth.HashRefreshToken(refreshToken))
	if errors.Is(err, pgx.ErrNoRows) {
		return pgtype.UUID{}, false, nil
	}
	if err != nil {
		return pgtype.UUID{}, false, fmt.Errorf("look up refresh token: %w", err)
	}
	// A client disconnect must not cancel the revoke halfway (#370).
	if err := s.tokens.RevokeRefreshFamily(context.WithoutCancel(ctx), rt.FamilyID); err != nil {
		return rt.UserID, true, fmt.Errorf("revoke refresh family: %w", err)
	}
	return rt.UserID, true, nil
}

// RevokeAll ends every session of a user after a security-relevant change;
// access tokens already issued expire within the access TTL. A failure is
// logged, not returned: the triggering change already succeeded.
func (s *Service) RevokeAll(ctx context.Context, userID pgtype.UUID) {
	if err := s.tokens.RevokeUserRefreshTokens(ctx, userID); err != nil {
		slog.Error("RevokeUserRefreshTokens failed", "user_id", userID.String(), "err", err)
	}
}

func (s *Service) issue(ctx context.Context, user db.User, family pgtype.UUID) (Pair, error) {
	access, err := auth.NewAccessTokenWithTTL(user.ID.String(), user.Role, s.cfg.JWTSecret, s.cfg.AccessTTL)
	if err != nil {
		return Pair{}, err
	}
	refresh, err := s.newRefresh(ctx, user, family)
	if err != nil {
		return Pair{}, err
	}
	return Pair{AccessToken: access, RefreshToken: refresh, ExpiresAt: time.Now().Add(s.cfg.AccessTTL)}, nil
}

func (s *Service) newRefresh(ctx context.Context, user db.User, family pgtype.UUID) (string, error) {
	refresh, hash, err := auth.GenerateRefreshToken()
	if err != nil {
		return "", err
	}
	if _, err := s.tokens.CreateRefreshToken(ctx, db.CreateRefreshTokenParams{
		UserID:    user.ID,
		TokenHash: hash,
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(s.cfg.RefreshTTL), Valid: true},
		FamilyID:  family,
	}); err != nil {
		return "", err
	}
	return refresh, nil
}
