// Package idptoken keeps the identity-provider tokens of each user's latest
// OIDC sign-in (#648). Catalog managers set to per-user identity authorize
// the user with the access token, which Baleia hands to Trino per query and
// refreshes with the refresh token. Tokens are sealed with the keyring, bound
// to the user, and are never logged or returned by the API.
package idptoken

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/crypto"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// Store is the subset of db.Querier the tokens use.
type Store interface {
	UpsertUserIDPToken(ctx context.Context, arg db.UpsertUserIDPTokenParams) error
	GetUserIDPToken(ctx context.Context, userID pgtype.UUID) (db.UserIdpToken, error)
	DeleteUserIDPToken(ctx context.Context, userID pgtype.UUID) error
}

// Refresher redeems a refresh token at the provider that issued it.
type Refresher interface {
	Refresh(ctx context.Context, providerID pgtype.UUID, refreshToken string) (auth.IDPTokens, error)
}

// ErrNoKeyring means tokens cannot be sealed: BALEIA_ENCRYPTION_KEY is unset.
var ErrNoKeyring = errors.New("idptoken: BALEIA_ENCRYPTION_KEY is not configured")

const (
	// renewBefore is how long before expiry a token is refreshed, so it
	// outlives the query it is handed to for planning.
	renewBefore = 2 * time.Minute
	// unknownExpiry is assumed when the provider does not say.
	unknownExpiry = 5 * time.Minute
)

// Tokens stores, refreshes and forgets users' identity-provider tokens.
type Tokens struct {
	store     Store
	keyring   *crypto.Keyring
	refresher Refresher
	logger    *slog.Logger
	now       func() time.Time

	locks sync.Map // user id -> *sync.Mutex: one refresh per user at a time
}

// New returns the tokens; refresher may be nil (no refresh: an expired token
// is dropped). logger nil discards.
func New(store Store, keyring *crypto.Keyring, refresher Refresher, logger *slog.Logger) *Tokens {
	return &Tokens{store: store, keyring: keyring, refresher: refresher, logger: cmp.Or(logger, slog.New(slog.DiscardHandler)), now: time.Now}
}

func aad(userID pgtype.UUID, kind string) string {
	return "user_idp_token:" + userID.String() + ":" + kind
}

// Save keeps the tokens of a sign-in, replacing the previous ones.
func (t *Tokens) Save(ctx context.Context, userID, providerID pgtype.UUID, tok auth.IDPTokens) error {
	if tok.AccessToken == "" {
		return t.Forget(ctx, userID)
	}
	if t.keyring == nil {
		return ErrNoKeyring
	}
	access, err := t.keyring.Seal([]byte(tok.AccessToken), aad(userID, "access"))
	if err != nil {
		return fmt.Errorf("seal access token: %w", err)
	}
	var refresh *string
	if tok.RefreshToken != "" {
		enc, err := t.keyring.Seal([]byte(tok.RefreshToken), aad(userID, "refresh"))
		if err != nil {
			return fmt.Errorf("seal refresh token: %w", err)
		}
		refresh = &enc
	}
	expiry := tok.Expiry
	if expiry.IsZero() {
		expiry = t.now().Add(unknownExpiry)
	}
	return t.store.UpsertUserIDPToken(ctx, db.UpsertUserIDPTokenParams{
		UserID: userID, AuthProviderID: providerID,
		AccessTokenEnc: access, RefreshTokenEnc: refresh,
		ExpiresAt: pgtype.Timestamptz{Time: expiry, Valid: true},
	})
}

// Forget deletes a user's tokens (sign-out, disabled account).
func (t *Tokens) Forget(ctx context.Context, userID pgtype.UUID) error {
	return t.store.DeleteUserIDPToken(ctx, userID)
}

// Access returns the user's access token, refreshed when it is about to
// expire. It returns "" when the user has none, or it expired and cannot be
// refreshed: the caller then acts without the user's identity.
func (t *Tokens) Access(ctx context.Context, userID pgtype.UUID) (string, error) {
	if !userID.Valid || t.keyring == nil {
		return "", nil
	}
	row, err := t.store.GetUserIDPToken(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("idp token: %w", err)
	}
	if t.fresh(row) {
		return t.open(row.AccessTokenEnc, userID, "access")
	}

	mu, _ := t.locks.LoadOrStore(userID.Bytes, &sync.Mutex{})
	lock, _ := mu.(*sync.Mutex)
	lock.Lock()
	defer lock.Unlock()
	// Another request may have refreshed it while this one waited.
	if row, err = t.store.GetUserIDPToken(ctx, userID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil
		}
		return "", fmt.Errorf("idp token: %w", err)
	}
	if t.fresh(row) {
		return t.open(row.AccessTokenEnc, userID, "access")
	}
	return t.refresh(ctx, userID, row)
}

func (t *Tokens) fresh(row db.UserIdpToken) bool {
	return row.ExpiresAt.Valid && t.now().Add(renewBefore).Before(row.ExpiresAt.Time)
}

func (t *Tokens) refresh(ctx context.Context, userID pgtype.UUID, row db.UserIdpToken) (string, error) {
	stillValid := row.ExpiresAt.Valid && t.now().Before(row.ExpiresAt.Time)
	if row.RefreshTokenEnc == nil || t.refresher == nil {
		if stillValid {
			return t.open(row.AccessTokenEnc, userID, "access")
		}
		return "", t.Forget(ctx, userID)
	}
	rt, err := t.open(*row.RefreshTokenEnc, userID, "refresh")
	if err != nil {
		return "", err
	}
	tok, err := t.refresher.Refresh(ctx, row.AuthProviderID, rt)
	if err != nil {
		// The provider ended the session (revoked, expired, user disabled):
		// the user signs in again to act as themselves.
		t.logger.WarnContext(ctx, "identity-provider token refresh failed", "user_id", userID.String(), "err", err)
		if stillValid {
			return t.open(row.AccessTokenEnc, userID, "access")
		}
		return "", t.Forget(ctx, userID)
	}
	if err := t.Save(ctx, userID, row.AuthProviderID, tok); err != nil {
		return "", err
	}
	return tok.AccessToken, nil
}

func (t *Tokens) open(enc string, userID pgtype.UUID, kind string) (string, error) {
	plain, err := t.keyring.Open(enc, aad(userID, kind))
	if err != nil {
		return "", fmt.Errorf("open %s token: %w", kind, err)
	}
	return string(plain), nil
}
