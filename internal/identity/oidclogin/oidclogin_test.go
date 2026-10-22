package oidclogin

import (
	"context"
	"errors"
	"net/url"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/crypto"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type fakeStore struct {
	providers map[[16]byte]db.AuthProvider
	attempts  map[string]db.OidcLoginAttempt
}

func (f *fakeStore) GetAuthProviderByID(_ context.Context, id pgtype.UUID) (db.AuthProvider, error) {
	if ap, ok := f.providers[id.Bytes]; ok {
		return ap, nil
	}
	return db.AuthProvider{}, pgx.ErrNoRows
}

func (f *fakeStore) CreateOIDCLoginAttempt(_ context.Context, arg db.CreateOIDCLoginAttemptParams) error {
	f.attempts[arg.State] = db.OidcLoginAttempt{State: arg.State, ProviderID: arg.ProviderID, Nonce: arg.Nonce, CodeVerifier: arg.CodeVerifier, Redirect: arg.Redirect}
	return nil
}

func (f *fakeStore) ConsumeOIDCLoginAttempt(_ context.Context, state string) (db.OidcLoginAttempt, error) {
	a, ok := f.attempts[state]
	if !ok {
		return db.OidcLoginAttempt{}, pgx.ErrNoRows
	}
	delete(f.attempts, state)
	return a, nil
}

type fakeAccounts struct{ bySubject map[string]db.User }

func (f *fakeAccounts) GetUserByExternalID(_ context.Context, arg db.GetUserByExternalIDParams) (db.User, error) {
	if u, ok := f.bySubject[*arg.ExternalID]; ok {
		return u, nil
	}
	return db.User{}, pgx.ErrNoRows
}

func (f *fakeAccounts) CreateUser(context.Context, db.CreateUserParams) (db.User, error) {
	return db.User{}, errors.New("not used")
}
func (f *fakeAccounts) UpdateUserRole(context.Context, db.UpdateUserRoleParams) error { return nil }
func (f *fakeAccounts) UpsertTeamMember(context.Context, db.UpsertTeamMemberParams) error {
	return nil
}

// fakeProvider accepts the code "good" and checks the PKCE verifier and nonce
// came back from the attempt.
type fakeProvider struct {
	subject   string
	challenge string
	nonce     string
}

func (p *fakeProvider) AuthURL(state, challenge, nonce string) (string, error) {
	p.challenge, p.nonce = challenge, nonce
	return "https://idp.example/authorize?state=" + url.QueryEscape(state), nil
}

func (p *fakeProvider) Exchange(_ context.Context, code, verifier, nonce string) (*auth.OIDCUserInfo, error) {
	if code != "good" || verifier == "" || nonce != p.nonce {
		return nil, errors.New("rejected")
	}
	return &auth.OIDCUserInfo{Subject: p.subject}, nil
}

type fixture struct {
	s         *Service
	store     *fakeStore
	provider  *fakeProvider
	id        pgtype.UUID
	discovers int
}

func newFixture(t *testing.T, status string) *fixture {
	t.Helper()
	kr, err := crypto.NewKeyring("test-secret-0123456789abcdef-0123456789", "")
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{id: pgtype.UUID{Bytes: uuid.New(), Valid: true}, provider: &fakeProvider{subject: "sub-1"}}
	enc, err := auth.SealOIDCConfig(kr, f.id.String(), auth.OIDCProviderConfig{IssuerURL: "https://idp.example", ClientID: "c", ClientSecret: "s"})
	if err != nil {
		t.Fatal(err)
	}
	f.store = &fakeStore{
		providers: map[[16]byte]db.AuthProvider{f.id.Bytes: {ID: f.id, Type: "oidc", ConfigEnc: enc, Enabled: true}},
		attempts:  map[string]db.OidcLoginAttempt{},
	}
	accounts := &fakeAccounts{bySubject: map[string]db.User{"sub-1": {ID: pgtype.UUID{Bytes: uuid.New(), Valid: true}, Status: status}}}
	discover := func(context.Context, auth.OIDCConfig) (Provider, error) {
		f.discovers++
		return f.provider, nil
	}
	f.s = New(f.store, accounts, kr, discover, Config{PublicURL: "https://baleia.example", CacheTTL: time.Hour})
	return f
}

func TestBeginFinish(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, "active")
	next := "/sql"
	authURL, state, err := f.s.Begin(ctx, f.id, &next)
	if err != nil || state == "" || authURL == "" {
		t.Fatalf("Begin = %q, %q, %v", authURL, state, err)
	}
	if f.provider.challenge == "" || f.provider.nonce == "" {
		t.Error("the authorization URL carries a PKCE challenge and a nonce")
	}

	user, redirect, err := f.s.Finish(ctx, f.id, state, state, "good")
	if err != nil || !user.ID.Valid || redirect == nil || *redirect != next {
		t.Fatalf("Finish = %+v, %v, %v", user, redirect, err)
	}
	if _, _, err := f.s.Finish(ctx, f.id, state, state, "good"); !errors.Is(err, ErrInvalidState) {
		t.Errorf("replayed state = %v, want ErrInvalidState", err)
	}
	if f.discovers != 1 {
		t.Errorf("discovered %d times, want the cached provider reused", f.discovers)
	}
}

func TestFinish_Rejections(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, "active")
	begin := func() string {
		t.Helper()
		_, state, err := f.s.Begin(ctx, f.id, nil)
		if err != nil {
			t.Fatal(err)
		}
		return state
	}

	if _, _, err := f.s.Finish(ctx, f.id, begin(), "other-cookie", "good"); !errors.Is(err, ErrStateMismatch) {
		t.Errorf("cookie mismatch = %v, want ErrStateMismatch", err)
	}
	if _, _, err := f.s.Finish(ctx, f.id, "", "", "good"); !errors.Is(err, ErrStateMismatch) {
		t.Errorf("empty state = %v, want ErrStateMismatch", err)
	}
	if _, _, err := f.s.Finish(ctx, f.id, "never-issued", "never-issued", "good"); !errors.Is(err, ErrInvalidState) {
		t.Errorf("unknown state = %v, want ErrInvalidState", err)
	}
	state := begin()
	if _, _, err := f.s.Finish(ctx, f.id, state, state, "bad"); !errors.Is(err, ErrExchange) {
		t.Errorf("bad code = %v, want ErrExchange", err)
	}
	if _, _, err := f.s.Finish(ctx, pgtype.UUID{Bytes: uuid.New(), Valid: true}, "s", "s", "good"); !errors.Is(err, ErrUnavailable) {
		t.Errorf("unknown provider = %v, want ErrUnavailable", err)
	}

	// A state issued for one provider cannot finish another's login.
	// The other provider is usable, so the rejection is the state check.
	other := pgtype.UUID{Bytes: uuid.New(), Valid: true}
	kr, err := crypto.NewKeyring("test-secret-0123456789abcdef-0123456789", "")
	if err != nil {
		t.Fatal(err)
	}
	enc, err := auth.SealOIDCConfig(kr, other.String(), auth.OIDCProviderConfig{IssuerURL: "https://idp.example", ClientID: "c", ClientSecret: "s"})
	if err != nil {
		t.Fatal(err)
	}
	f.store.providers[other.Bytes] = db.AuthProvider{ID: other, Type: "oidc", ConfigEnc: enc, Enabled: true}
	state = begin()
	if _, _, err := f.s.Finish(ctx, other, state, state, "good"); !errors.Is(err, ErrStateMismatch) {
		t.Errorf("a state of another provider = %v, want ErrStateMismatch", err)
	}
}

func TestFinish_Suspended(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, "suspended")
	_, state, err := f.s.Begin(ctx, f.id, nil)
	if err != nil {
		t.Fatal(err)
	}
	user, _, err := f.s.Finish(ctx, f.id, state, state, "good")
	if !errors.Is(err, ErrSuspended) || !user.ID.Valid {
		t.Fatalf("Finish = %+v, %v; want ErrSuspended with the account", user, err)
	}
}

// TestProvider_FailsClosed: a configuration that is not sealed is refused,
// never parsed as plaintext (#278).
func TestProvider_FailsClosed(t *testing.T) {
	f := newFixture(t, "active")
	ap := f.store.providers[f.id.Bytes]
	ap.ConfigEnc = `{"issuer_url":"http://issuer.example","client_id":"c","client_secret":"s"}`
	ap.UpdatedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
	f.store.providers[f.id.Bytes] = ap
	if _, _, err := f.s.Provider(context.Background(), f.id); err == nil {
		t.Fatal("a plaintext config was accepted")
	}
}

// TestProvider_OnlyEnabledOIDC: a disabled provider, or one of another type,
// cannot start or finish a login.
func TestProvider_OnlyEnabledOIDC(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		name    string
		typ     string
		enabled bool
	}{
		{"disabled", "oidc", false},
		{"ldap", "ldap", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t, "active")
			ap := f.store.providers[f.id.Bytes]
			ap.Type, ap.Enabled = c.typ, c.enabled
			f.store.providers[f.id.Bytes] = ap
			if _, _, err := f.s.Begin(ctx, f.id, nil); !errors.Is(err, ErrUnavailable) {
				t.Errorf("Begin = %v, want ErrUnavailable", err)
			}
			if _, _, err := f.s.Finish(ctx, f.id, "s", "s", "good"); !errors.Is(err, ErrUnavailable) {
				t.Errorf("Finish = %v, want ErrUnavailable", err)
			}
			if f.discovers != 0 {
				t.Error("an unusable provider must not be discovered")
			}
		})
	}
}
