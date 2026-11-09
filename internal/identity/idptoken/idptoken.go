// Package idptoken keeps the identity-provider tokens of each user's latest
// OIDC sign-in (#648). Catalog managers set to per-user identity authorize
// the user with the access token, which Baleia hands to Trino per statement
// and refreshes with the refresh token. Tokens are sealed with the keyring,
// bound to the user, and are never logged or returned by the API.
//
// The API and the worker both refresh. A refresh runs in a transaction that
// locks the user's row, so one process refreshes while the others wait and
// read its result (a provider that rotates refresh tokens rejects a reused
// one), and a sign-out or suspension that deletes the row is never undone by
// a refresh in flight.
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
)

// Store is the subset of db.Querier the tokens use.
type Store interface {
	SaveUserIDPTokenOnSignIn(ctx context.Context, arg db.SaveUserIDPTokenOnSignInParams) (int64, error)
	GetUserIDPToken(ctx context.Context, userID db.UUID) (db.UserIdpToken, error)
	GetUserIDPTokenForUpdate(ctx context.Context, userID db.UUID) (db.UserIdpToken, error)
	UpdateUserIDPToken(ctx context.Context, arg db.UpdateUserIDPTokenParams) error
	DeleteUserIDPToken(ctx context.Context, userID db.UUID) error
}

// Tx runs fn in one database transaction over a Store.
type Tx func(ctx context.Context, fn func(Store) error) error

// Refresher redeems a refresh token at the provider that issued it.
type Refresher interface {
	Refresh(ctx context.Context, providerID db.UUID, refreshToken string) (auth.IDPTokens, error)
}

// ErrNoKeyring means tokens cannot be sealed: BALEIA_ENCRYPTION_KEY is unset.
var ErrNoKeyring = errors.New("idptoken: BALEIA_ENCRYPTION_KEY is not configured")

const (
	// renewBefore is how long before expiry a token is refreshed.
	renewBefore = 2 * time.Minute
	// unknownExpiry is assumed when the provider does not say.
	unknownExpiry = 5 * time.Minute
	// stripes bounds the in-process locks that keep one process from opening
	// several refresh transactions for the same user.
	stripes = 64
)

// Tokens stores, refreshes and forgets users' identity-provider tokens.
type Tokens struct {
	store     Store
	tx        Tx
	keyring   *crypto.Keyring
	refresher Refresher
	logger    *slog.Logger
	now       func() time.Time

	locks [stripes]sync.Mutex
}

// New returns the tokens. refresher may be nil (no refresh: an expired token
// is dropped); logger nil discards.
func New(store Store, tx Tx, keyring *crypto.Keyring, refresher Refresher, logger *slog.Logger) *Tokens {
	return &Tokens{store: store, tx: tx, keyring: keyring, refresher: refresher, logger: cmp.Or(logger, slog.New(slog.DiscardHandler)), now: time.Now}
}

func aad(userID db.UUID, kind string) string {
	return "user_idp_token:" + userID.String() + ":" + kind
}

type sealed struct {
	access  string
	refresh *string
	expiry  db.Timestamptz
}

func (t *Tokens) seal(userID db.UUID, tok auth.IDPTokens) (sealed, error) {
	if t.keyring == nil {
		return sealed{}, ErrNoKeyring
	}
	access, err := t.keyring.Seal([]byte(tok.AccessToken), aad(userID, "access"))
	if err != nil {
		return sealed{}, fmt.Errorf("seal access token: %w", err)
	}
	out := sealed{access: access}
	if tok.RefreshToken != "" {
		enc, err := t.keyring.Seal([]byte(tok.RefreshToken), aad(userID, "refresh"))
		if err != nil {
			return sealed{}, fmt.Errorf("seal refresh token: %w", err)
		}
		out.refresh = &enc
	}
	expiry := tok.Expiry
	if expiry.IsZero() {
		expiry = t.now().Add(unknownExpiry)
	}
	out.expiry = db.Timestamptz{Time: expiry, Valid: true}
	return out, nil
}

// Save keeps the tokens of a sign-in, replacing the previous ones. It keeps
// nothing for a suspended account or a disabled provider, and a sign-in
// without an access token clears the previous one.
func (t *Tokens) Save(ctx context.Context, userID, providerID db.UUID, tok auth.IDPTokens) error {
	if tok.AccessToken == "" {
		return t.Forget(ctx, userID)
	}
	s, err := t.seal(userID, tok)
	if err != nil {
		return err
	}
	_, err = t.store.SaveUserIDPTokenOnSignIn(ctx, db.SaveUserIDPTokenOnSignInParams{
		UserID: userID, AuthProviderID: providerID,
		AccessTokenEnc: s.access, RefreshTokenEnc: s.refresh, ExpiresAt: s.expiry,
	})
	return err
}

// Forget deletes a user's tokens (sign-out, failed sign-in save). A refresh
// in flight finishes first; it cannot write the row back.
func (t *Tokens) Forget(ctx context.Context, userID db.UUID) error {
	return t.store.DeleteUserIDPToken(ctx, userID)
}

// Access returns the user's access token, refreshed when it is about to
// expire. It returns "" when the user has none, or it expired and cannot be
// refreshed: the caller then acts without the user's identity.
func (t *Tokens) Access(ctx context.Context, userID db.UUID) (string, error) {
	if !userID.Valid || t.keyring == nil {
		return "", nil
	}
	row, err := t.store.GetUserIDPToken(ctx, userID)
	if errors.Is(err, db.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("idp token: %w", err)
	}
	if t.fresh(row) {
		return t.open(row.AccessTokenEnc, userID, "access")
	}

	lock := &t.locks[int(userID.Bytes[15])%stripes]
	lock.Lock()
	defer lock.Unlock()
	var token string
	err = t.tx(ctx, func(q Store) error {
		row, err := q.GetUserIDPTokenForUpdate(ctx, userID)
		if errors.Is(err, db.ErrNoRows) {
			return nil // signed out meanwhile
		}
		if err != nil {
			return fmt.Errorf("idp token: %w", err)
		}
		if t.fresh(row) { // another process refreshed it
			token, err = t.open(row.AccessTokenEnc, userID, "access")
			return err
		}
		token, err = t.refresh(ctx, q, userID, row)
		return err
	})
	return token, err
}

func (t *Tokens) fresh(row db.UserIdpToken) bool {
	return row.ExpiresAt.Valid && t.now().Add(renewBefore).Before(row.ExpiresAt.Time)
}

// refresh runs inside the transaction that locked row.
func (t *Tokens) refresh(ctx context.Context, q Store, userID db.UUID, row db.UserIdpToken) (string, error) {
	stillValid := row.ExpiresAt.Valid && t.now().Before(row.ExpiresAt.Time)
	if row.RefreshTokenEnc == nil || t.refresher == nil {
		if stillValid {
			return t.open(row.AccessTokenEnc, userID, "access")
		}
		return "", q.DeleteUserIDPToken(ctx, userID)
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
		return "", q.DeleteUserIDPToken(ctx, userID)
	}
	if tok.RefreshToken == "" {
		tok.RefreshToken = rt
	}
	s, err := t.seal(userID, tok)
	if err != nil {
		return "", err
	}
	if err := q.UpdateUserIDPToken(ctx, db.UpdateUserIDPTokenParams{
		UserID: userID, AccessTokenEnc: s.access, RefreshTokenEnc: s.refresh, ExpiresAt: s.expiry,
	}); err != nil {
		return "", fmt.Errorf("idp token: %w", err)
	}
	return tok.AccessToken, nil
}

func (t *Tokens) open(enc string, userID db.UUID, kind string) (string, error) {
	plain, err := t.keyring.Open(enc, aad(userID, kind))
	if err != nil {
		return "", fmt.Errorf("open %s token: %w", kind, err)
	}
	return string(plain), nil
}
