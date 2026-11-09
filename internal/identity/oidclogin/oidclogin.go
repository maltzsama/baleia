// Package oidclogin runs the OIDC authorization-code login (#278): it starts
// an attempt bound to a state, a PKCE verifier and a nonce, and finishes it by
// exchanging the code and provisioning the account.
package oidclogin

import (
	"cmp"
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/crypto"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/identity/provision"
	"github.com/jackc/pgx/v5/pgtype"
)

// Store keeps providers and login attempts; *repo.Repo implements it.
type Store interface {
	GetAuthProviderByID(ctx context.Context, id pgtype.UUID) (db.AuthProvider, error)
	CreateOIDCLoginAttempt(ctx context.Context, arg db.CreateOIDCLoginAttemptParams) error
	ConsumeOIDCLoginAttempt(ctx context.Context, state string) (db.OidcLoginAttempt, error)
	ListAuthProviders(ctx context.Context) ([]db.AuthProvider, error)
	GetServiceAccountByClient(ctx context.Context, arg db.GetServiceAccountByClientParams) (db.User, error)
}

// Provider is a discovered identity provider.
type Provider interface {
	AuthURL(state, challenge, nonce string) (string, error)
	Exchange(ctx context.Context, code, verifier, nonce string) (*auth.OIDCUserInfo, error)
	Refresh(ctx context.Context, refreshToken string) (auth.IDPTokens, error)
	ClientAccessToken(ctx context.Context, raw string) (clientID string, expiry time.Time, err error)
}

// TokenSaver keeps the identity-provider tokens of a sign-in (#648).
type TokenSaver interface {
	Save(ctx context.Context, userID, providerID pgtype.UUID, tok auth.IDPTokens) error
	Forget(ctx context.Context, userID pgtype.UUID) error
}

// Discover reaches an issuer and returns its provider.
type Discover func(ctx context.Context, cfg auth.OIDCConfig) (Provider, error)

// DiscoverIssuer discovers the issuer over the network.
func DiscoverIssuer(ctx context.Context, cfg auth.OIDCConfig) (Provider, error) {
	return auth.NewOIDCProvider(ctx, cfg)
}

var (
	// ErrUnavailable: the provider is unknown, unreadable or unreachable.
	ErrUnavailable = errors.New("oidclogin: identity provider unavailable")
	// ErrStateMismatch: the callback's state is missing, differs from the
	// browser's cookie, or belongs to another provider.
	ErrStateMismatch = errors.New("oidclogin: state mismatch")
	// ErrInvalidState: no live attempt has this state (expired or replayed).
	ErrInvalidState = errors.New("oidclogin: invalid state")
	// ErrExchange: the provider rejected the code or the ID token.
	ErrExchange = errors.New("oidclogin: code exchange failed")
	// ErrSuspended: the account exists but is suspended; OIDC reports it on
	// the login page (#284).
	ErrSuspended = errors.New("oidclogin: account suspended")
)

// Config are the login settings, resolved by the caller.
type Config struct {
	// PublicURL is the base of the callback URL registered with providers.
	PublicURL string
	// CacheTTL is how long a discovered provider is reused.
	CacheTTL time.Duration
	// Logger receives the logs; nil discards them (#379).
	Logger *slog.Logger
}

// Service starts and finishes OIDC logins.
type Service struct {
	store    Store
	accounts provision.Accounts
	keyring  *crypto.Keyring
	discover Discover
	cfg      Config
	// cache holds discovered providers keyed by id + config version (#280).
	cache sync.Map
	// tokens keeps each sign-in's tokens; nil keeps none.
	tokens TokenSaver
}

// SetTokens makes Finish keep each sign-in's identity-provider tokens, which
// catalog managers set to per-user identity accept (#648).
func (s *Service) SetTokens(t TokenSaver) { s.tokens = t }

// Refresh redeems a refresh token at the provider that issued it (#648).
func (s *Service) Refresh(ctx context.Context, providerID pgtype.UUID, refreshToken string) (auth.IDPTokens, error) {
	provider, _, err := s.Provider(ctx, providerID)
	if err != nil {
		return auth.IDPTokens{}, ErrUnavailable
	}
	return provider.Refresh(ctx, refreshToken)
}

type cacheEntry struct {
	provider Provider
	cfg      auth.OIDCProviderConfig
	expires  time.Time
}

// New builds the service; keyring opens the sealed provider configurations.
func New(store Store, accounts provision.Accounts, keyring *crypto.Keyring, discover Discover, cfg Config) *Service {
	cfg.Logger = cmp.Or(cfg.Logger, slog.New(slog.DiscardHandler))
	return &Service{store: store, accounts: accounts, keyring: keyring, discover: discover, cfg: cfg}
}

// Provider returns the discovered provider and its configuration, reusing a
// discovery until the configuration changes or the cache entry expires. Only
// an enabled OIDC provider is usable, and a configuration that cannot be
// opened fails closed (#278).
func (s *Service) Provider(ctx context.Context, providerID pgtype.UUID) (Provider, auth.OIDCProviderConfig, error) {
	ap, err := s.store.GetAuthProviderByID(ctx, providerID)
	if err != nil {
		return nil, auth.OIDCProviderConfig{}, err
	}
	if ap.Type != "oidc" || !ap.Enabled {
		return nil, auth.OIDCProviderConfig{}, ErrUnavailable
	}
	version := ap.ConfigEnc
	if ap.UpdatedAt.Valid {
		version = ap.UpdatedAt.Time.UTC().Format(time.RFC3339Nano)
	}
	key := providerID.String() + ":" + version
	if v, ok := s.cache.Load(key); ok {
		if entry, ok := v.(*cacheEntry); ok && time.Now().Before(entry.expires) {
			return entry.provider, entry.cfg, nil
		}
		s.cache.Delete(key)
	}

	cfg, err := auth.OpenOIDCConfig(s.keyring, ap.ID.String(), ap.ConfigEnc)
	if err != nil {
		return nil, auth.OIDCProviderConfig{}, err
	}
	provider, err := s.discover(ctx, auth.OIDCConfig{
		IssuerURL:    cfg.IssuerURL,
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		RedirectURL:  s.cfg.PublicURL + "/api/v1/auth/oidc/" + providerID.String() + "/callback",
		Claims:       cfg.Claims,
	})
	if err != nil {
		return nil, auth.OIDCProviderConfig{}, err
	}
	s.cache.Store(key, &cacheEntry{provider: provider, cfg: cfg, expires: time.Now().Add(s.cfg.CacheTTL)})
	return provider, cfg, nil
}

// Begin records a login attempt and returns the provider's authorization URL
// and the state the browser must present on the callback. redirect is where
// to land after the login; the caller has validated it.
func (s *Service) Begin(ctx context.Context, providerID pgtype.UUID, redirect *string) (authURL, state string, err error) {
	provider, _, err := s.Provider(ctx, providerID)
	if err != nil {
		return "", "", ErrUnavailable
	}
	verifier, challenge, err := auth.GeneratePKCE()
	if err != nil {
		return "", "", err
	}
	nonce, err := auth.GenerateNonce()
	if err != nil {
		return "", "", err
	}
	state = newState()
	if err := s.store.CreateOIDCLoginAttempt(ctx, db.CreateOIDCLoginAttemptParams{
		State:        state,
		ProviderID:   providerID,
		Nonce:        &nonce,
		CodeVerifier: verifier,
		Redirect:     redirect,
		ExpiresAt:    pgtype.Timestamptz{Time: time.Now().Add(auth.OIDCAttemptTTL), Valid: true},
	}); err != nil {
		return "", "", err
	}
	authURL, err = provider.AuthURL(state, challenge, nonce)
	if err != nil {
		return "", "", err
	}
	return authURL, state, nil
}

// Finish completes the attempt for state: it checks the state against the
// browser's cookie, consumes the attempt once, exchanges the code and
// provisions the account. It returns the account and the attempt's redirect.
// On ErrSuspended the account is returned too, for the audit trail.
func (s *Service) Finish(ctx context.Context, providerID pgtype.UUID, state, cookieState, code string) (db.User, *string, error) {
	provider, cfg, err := s.Provider(ctx, providerID)
	if err != nil {
		return db.User{}, nil, ErrUnavailable
	}
	if state == "" || cookieState != state {
		return db.User{}, nil, ErrStateMismatch
	}
	attempt, err := s.store.ConsumeOIDCLoginAttempt(ctx, state)
	if err != nil {
		return db.User{}, nil, ErrInvalidState
	}
	if attempt.ProviderID != providerID {
		return db.User{}, nil, ErrStateMismatch
	}

	nonce := ""
	if attempt.Nonce != nil {
		nonce = *attempt.Nonce
	}
	info, err := provider.Exchange(ctx, code, attempt.CodeVerifier, nonce)
	if err != nil {
		return db.User{}, nil, ErrExchange
	}

	user, err := provision.OIDCUser(ctx, s.cfg.Logger, s.accounts, providerID, cfg, info)
	if err != nil {
		return db.User{}, nil, err
	}
	if err := auth.RequireActive(user.Status); err != nil {
		return user, nil, ErrSuspended
	}
	// The sign-in still succeeds without them: the user then acts as the
	// connection's own account on catalogs set to per-user identity.
	if s.tokens != nil {
		if err := s.tokens.Save(ctx, user.ID, providerID, info.Tokens); err != nil {
			s.cfg.Logger.WarnContext(ctx, "keep identity-provider tokens", "user_id", user.ID.String(), "err", err)
			// Never leave the previous sign-in's token in use.
			if err := s.tokens.Forget(ctx, user.ID); err != nil {
				s.cfg.Logger.WarnContext(ctx, "forget identity-provider tokens", "user_id", user.ID.String(), "err", err)
			}
		}
	}
	return user, attempt.Redirect, nil
}

// newState is 128 random bits, URL-safe and unpadded.
func newState() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// ErrNoServiceAccount means a bearer token is not a client-credentials token
// of an enabled provider for an active service account.
var ErrNoServiceAccount = errors.New("oidclogin: no service account for this token")

// ServiceAccountForToken authenticates a service account by its identity
// provider's client-credentials access token (#653): the token must be a JWT
// signed by an enabled OIDC provider whose issuer it names, for a client a
// service account is linked to. The token is kept as the service account's
// identity-provider token for per-user catalogs (ADR 0015).
func (s *Service) ServiceAccountForToken(ctx context.Context, raw string) (db.User, error) {
	iss := strings.TrimRight(auth.TokenIssuer(raw), "/")
	if iss == "" {
		return db.User{}, ErrNoServiceAccount
	}
	aps, err := s.store.ListAuthProviders(ctx)
	if err != nil {
		return db.User{}, err
	}
	for _, ap := range aps {
		if ap.Type != "oidc" {
			continue
		}
		provider, cfg, err := s.Provider(ctx, ap.ID)
		if err != nil || strings.TrimRight(cfg.IssuerURL, "/") != iss {
			continue
		}
		clientID, expiry, err := provider.ClientAccessToken(ctx, raw)
		if err != nil {
			return db.User{}, ErrNoServiceAccount
		}
		sa, err := s.store.GetServiceAccountByClient(ctx, db.GetServiceAccountByClientParams{AuthProviderID: ap.ID, ExternalID: "client:" + clientID})
		if err != nil {
			return db.User{}, ErrNoServiceAccount
		}
		if s.tokens != nil {
			if err := s.tokens.Save(ctx, sa.ID, ap.ID, auth.IDPTokens{AccessToken: raw, Expiry: expiry}); err != nil {
				s.cfg.Logger.WarnContext(ctx, "keep a service account's identity-provider token", "user_id", sa.ID.String(), "err", err)
			}
		}
		return sa, nil
	}
	return db.User{}, ErrNoServiceAccount
}
