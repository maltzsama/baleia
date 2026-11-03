// Package catalogidentity decides which bearer token a query session hands to
// catalog managers set to per-user identity (#648, docs/adr/0015):
//
//   - a user signed in through an identity provider carries their own
//     access token, and the catalog manager authorizes them;
//   - a user signed in without one (local or LDAP accounts) and work without
//     a user carry the token of the connection behind the session's catalog,
//     and the catalog manager decides as that account;
//   - a user signed in through an identity provider whose token expired and
//     cannot be refreshed carries none: the catalog manager refuses, and the
//     user signs in again. They never fall back to the connection's account,
//     which may reach more than they do.
package catalogidentity

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/galileostudio/baleia/internal/catalogs"
	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/connections/providers"
	"github.com/galileostudio/baleia/internal/crypto"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/queryengine"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// Store is the subset of db.Querier the resolver reads.
type Store interface {
	GetUserByID(ctx context.Context, id pgtype.UUID) (db.User, error)
	GetManagedConnectionOfEngineCatalog(ctx context.Context, arg db.GetManagedConnectionOfEngineCatalogParams) (db.CatalogConnection, error)
}

// UserTokens returns a user's identity-provider access token, "" when none
// is usable (idptoken.Tokens).
type UserTokens interface {
	Access(ctx context.Context, userID pgtype.UUID) (string, error)
}

// serviceTokenFallback bounds how long a token with no stated expiry (a
// static bearer token) is reused before the connection is read again.
const serviceTokenFallback = 5 * time.Minute

// Resolver implements queryengine.CatalogCredentials.
type Resolver struct {
	store     Store
	keyring   *crypto.Keyring
	providers *connections.Registry
	users     UserTokens
	logger    *slog.Logger
	now       func() time.Time

	mu    sync.Mutex
	cache map[string]serviceToken // connection id + version -> token
}

type serviceToken struct {
	token  string
	expiry time.Time
}

var _ queryengine.CatalogCredentials = (*Resolver)(nil)

// New returns a resolver. logger nil discards.
func New(store Store, keyring *crypto.Keyring, registry *connections.Registry, users UserTokens, logger *slog.Logger) *Resolver {
	return &Resolver{
		store: store, keyring: keyring, providers: registry, users: users,
		logger: cmp.Or(logger, slog.New(slog.DiscardHandler)), now: time.Now,
		cache: map[string]serviceToken{},
	}
}

// CatalogToken returns the session's bearer token for catalog managers set
// to per-user identity; "" when the session needs none.
func (r *Resolver) CatalogToken(ctx context.Context, engineID string, spec queryengine.SessionSpec) (string, error) {
	if p := spec.Identity.Principal; p != "" {
		uid, err := uuid.Parse(p)
		if err != nil {
			return "", fmt.Errorf("catalog identity: principal: %w", err)
		}
		id := pgtype.UUID{Bytes: uid, Valid: true}
		user, err := r.store.GetUserByID(ctx, id)
		if err != nil {
			return "", fmt.Errorf("catalog identity: principal: %w", err)
		}
		if user.AuthProvider == "oidc" {
			tok, err := r.users.Access(ctx, id)
			if err != nil {
				return "", fmt.Errorf("catalog identity: user token: %w", err)
			}
			if tok == "" {
				r.logger.InfoContext(ctx, "no identity-provider token: per-user catalogs will refuse the session until the user signs in again", "user_id", p)
			}
			return tok, nil
		}
	}
	return r.connectionToken(ctx, engineID, spec.Catalog)
}

// connectionToken is the token of the per-user connection behind catalog.
func (r *Resolver) connectionToken(ctx context.Context, engineID, catalog string) (string, error) {
	if catalog == "" {
		return "", nil
	}
	eid, err := uuid.Parse(engineID)
	if err != nil {
		return "", nil // not a stored engine (a transient test engine)
	}
	conn, err := r.store.GetManagedConnectionOfEngineCatalog(ctx, db.GetManagedConnectionOfEngineCatalogParams{
		EngineID: pgtype.UUID{Bytes: eid, Valid: true}, EngineCatalogName: catalog,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("catalog identity: connection of %s: %w", catalog, err)
	}
	p, err := r.providers.Get(connections.Kind(conn.Type))
	if err != nil {
		return "", nil
	}
	ui, ok := p.(connections.UserIdentity)
	if !ok {
		return "", nil
	}
	key := conn.ID.String() + "@" + conn.UpdatedAt.Time.UTC().Format(time.RFC3339Nano)
	r.mu.Lock()
	cached, hit := r.cache[key]
	r.mu.Unlock()
	if hit && r.now().Before(cached.expiry) {
		return cached.token, nil
	}

	plain, err := catalogs.OpenCredentials(conn, r.keyring)
	if err != nil {
		return "", fmt.Errorf("catalog identity: credentials of %s: %w", catalog, err)
	}
	cfg, err := providers.FromStored(r.providers, connections.Kind(conn.Type), conn.ID, conn.Url, conn.Config, plain)
	if err != nil {
		return "", fmt.Errorf("catalog identity: config of %s: %w", catalog, err)
	}
	if !ui.UserIdentity(cfg) {
		r.remember(key, "", r.now().Add(serviceTokenFallback))
		return "", nil
	}
	tok, expiry, err := ui.ServiceToken(ctx, cfg)
	if err != nil {
		return "", fmt.Errorf("catalog identity: token of the %s connection: %w", catalog, err)
	}
	if expiry.IsZero() {
		expiry = r.now().Add(serviceTokenFallback)
	}
	r.remember(key, tok, expiry)
	return tok, nil
}

func (r *Resolver) remember(key, tok string, expiry time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cache[key] = serviceToken{token: tok, expiry: expiry}
}
