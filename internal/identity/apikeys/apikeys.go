// Package apikeys manages a user's personal API keys. The secret is shown
// once at creation; only its hash and a short prefix are stored (#276).
package apikeys

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"time"

	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/jackc/pgx/v5/pgtype"
)

// Store persists API keys; *db.Queries implements it.
type Store interface {
	ListAPIKeysByUser(ctx context.Context, userID pgtype.UUID) ([]db.ApiKey, error)
	CreateAPIKey(ctx context.Context, arg db.CreateAPIKeyParams) (db.ApiKey, error)
	DeleteAPIKey(ctx context.Context, arg db.DeleteAPIKeyParams) error
}

// ErrNotFound: the user has no key with this id.
var ErrNotFound = errors.New("apikeys: not found")

// Service manages API keys.
type Service struct{ store Store }

// New builds the service.
func New(store Store) *Service { return &Service{store: store} }

// List returns the user's keys.
func (s *Service) List(ctx context.Context, userID pgtype.UUID) ([]db.ApiKey, error) {
	return s.store.ListAPIKeysByUser(ctx, userID)
}

// Create issues a key for the user and returns it with its secret, which is
// never stored nor available again. A nil expiresAt never expires.
func (s *Service) Create(ctx context.Context, userID pgtype.UUID, name string, expiresAt *time.Time) (db.ApiKey, string, error) {
	secret := newSecret()
	var expires pgtype.Timestamptz
	if expiresAt != nil {
		expires = pgtype.Timestamptz{Time: *expiresAt, Valid: true}
	}
	key, err := s.store.CreateAPIKey(ctx, db.CreateAPIKeyParams{
		UserID:    userID,
		Name:      name,
		KeyHash:   auth.HashAPIKey(secret),
		Prefix:    secret[:8],
		Scopes:    []string{"*"},
		ExpiresAt: expires,
	})
	if err != nil {
		return db.ApiKey{}, "", err
	}
	return key, secret, nil
}

// Delete revokes one of the user's keys; another user's key is not found.
func (s *Service) Delete(ctx context.Context, userID, keyID pgtype.UUID) error {
	if err := s.store.DeleteAPIKey(ctx, db.DeleteAPIKeyParams{ID: keyID, UserID: userID}); err != nil {
		return ErrNotFound
	}
	return nil
}

// newSecret is "bal_" and 256 random bits, URL-safe and unpadded.
func newSecret() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return "bal_" + base64.RawURLEncoding.EncodeToString(b)
}
