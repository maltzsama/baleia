package idptoken

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/crypto"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// memStore stands in for the database: rowLock plays the row lock a refresh
// transaction takes, shared by every Tokens over the same store.
type memStore struct {
	mu        sync.Mutex
	rowLock   sync.Mutex
	rows      map[[16]byte]db.UserIdpToken
	suspended map[[16]byte]bool
}

func newMem() *memStore {
	return &memStore{rows: map[[16]byte]db.UserIdpToken{}, suspended: map[[16]byte]bool{}}
}

func (m *memStore) tx(_ context.Context, fn func(Store) error) error {
	m.rowLock.Lock()
	defer m.rowLock.Unlock()
	return fn(m)
}

func (m *memStore) SaveUserIDPTokenOnSignIn(_ context.Context, a db.SaveUserIDPTokenOnSignInParams) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.suspended[a.UserID.Bytes] {
		return 0, nil
	}
	m.rows[a.UserID.Bytes] = db.UserIdpToken{UserID: a.UserID, AuthProviderID: a.AuthProviderID, AccessTokenEnc: a.AccessTokenEnc, RefreshTokenEnc: a.RefreshTokenEnc, ExpiresAt: a.ExpiresAt}
	return 1, nil
}

func (m *memStore) get(id pgtype.UUID) (db.UserIdpToken, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.rows[id.Bytes]
	if !ok {
		return db.UserIdpToken{}, pgx.ErrNoRows
	}
	return r, nil
}

func (m *memStore) GetUserIDPToken(_ context.Context, id pgtype.UUID) (db.UserIdpToken, error) {
	return m.get(id)
}

func (m *memStore) GetUserIDPTokenForUpdate(_ context.Context, id pgtype.UUID) (db.UserIdpToken, error) {
	return m.get(id)
}

func (m *memStore) UpdateUserIDPToken(_ context.Context, a db.UpdateUserIDPTokenParams) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.rows[a.UserID.Bytes]
	if !ok {
		return nil // an UPDATE never recreates a deleted row
	}
	r.AccessTokenEnc, r.RefreshTokenEnc, r.ExpiresAt = a.AccessTokenEnc, a.RefreshTokenEnc, a.ExpiresAt
	m.rows[a.UserID.Bytes] = r
	return nil
}

func (m *memStore) DeleteUserIDPToken(_ context.Context, id pgtype.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.rows, id.Bytes)
	return nil
}

// rotatingRefresher accepts each refresh token once and issues the next, as a
// provider with refresh-token rotation does.
type rotatingRefresher struct {
	mu    sync.Mutex
	valid string
	n     int
	calls atomic.Int32
	err   error
}

func (r *rotatingRefresher) Refresh(_ context.Context, _ pgtype.UUID, rt string) (auth.IDPTokens, error) {
	r.calls.Add(1)
	if r.err != nil {
		return auth.IDPTokens{}, r.err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if rt != r.valid {
		return auth.IDPTokens{}, errors.New("invalid_grant: refresh token reused")
	}
	r.n++
	r.valid = fmt.Sprintf("rt-%d", r.n)
	return auth.IDPTokens{AccessToken: fmt.Sprintf("access-%d", r.n), RefreshToken: r.valid, Expiry: time.Now().Add(time.Hour)}, nil
}

func keyring(t *testing.T) *crypto.Keyring {
	t.Helper()
	kr, err := crypto.NewKeyring("test-secret-0123456789abcdef-0123456789", "")
	if err != nil {
		t.Fatal(err)
	}
	return kr
}

func newTokens(t *testing.T, st *memStore, r Refresher) *Tokens {
	t.Helper()
	return New(st, st.tx, keyring(t), r, nil)
}

func id() pgtype.UUID { return pgtype.UUID{Bytes: uuid.New(), Valid: true} }

func TestTokens_SaveSealsAndReturnsFresh(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newMem()
	tk := newTokens(t, st, nil)
	user, prov := id(), id()
	if err := tk.Save(ctx, user, prov, auth.IDPTokens{AccessToken: "secret-access", RefreshToken: "secret-refresh", Expiry: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	row := st.rows[user.Bytes]
	if strings.Contains(row.AccessTokenEnc, "secret-access") || row.RefreshTokenEnc == nil || strings.Contains(*row.RefreshTokenEnc, "secret-refresh") {
		t.Fatal("tokens must be stored sealed")
	}
	if got, err := tk.Access(ctx, user); err != nil || got != "secret-access" {
		t.Fatalf("Access = %q, %v", got, err)
	}
	if got, err := tk.Access(ctx, id()); err != nil || got != "" {
		t.Errorf("unknown user = %q, %v", got, err)
	}
	// A sealed token is bound to its user: another user's row does not open.
	other := id()
	row.UserID = other
	st.rows[other.Bytes] = row
	if _, err := tk.Access(ctx, other); err == nil {
		t.Error("a token moved to another user must not open")
	}
}

func TestTokens_SaveSkipsSuspendedAccount(t *testing.T) {
	t.Parallel()
	st := newMem()
	tk := newTokens(t, st, nil)
	user := id()
	st.suspended[user.Bytes] = true
	if err := tk.Save(context.Background(), user, id(), auth.IDPTokens{AccessToken: "a", Expiry: time.Now().Add(time.Hour)}); err != nil || len(st.rows) != 0 {
		t.Fatalf("Save = %v, rows %d", err, len(st.rows))
	}
}

// TestTokens_RefreshAcrossInstances: the API and the worker refresh the same
// user at once against a provider that rotates refresh tokens. One refresh
// reaches the provider; the other process reads its result instead of
// reusing the old refresh token, which would end the user's session.
func TestTokens_RefreshAcrossInstances(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newMem()
	r := &rotatingRefresher{valid: "rt-0"}
	api, worker := newTokens(t, st, r), newTokens(t, st, r)
	user := id()
	if err := api.Save(ctx, user, id(), auth.IDPTokens{AccessToken: "old", RefreshToken: "rt-0", Expiry: time.Now().Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := range 16 {
		tk := api
		if i%2 == 1 {
			tk = worker
		}
		wg.Go(func() {
			if got, err := tk.Access(ctx, user); err != nil || got != "access-1" {
				t.Errorf("Access = %q, %v", got, err)
			}
		})
	}
	wg.Wait()
	if n := r.calls.Load(); n != 1 {
		t.Errorf("provider saw %d refreshes, want 1", n)
	}
	if _, ok := st.rows[user.Bytes]; !ok {
		t.Error("the refreshed token was deleted")
	}
}

func TestTokens_ExpiredWithoutRefreshIsDropped(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newMem()
	tk := newTokens(t, st, &rotatingRefresher{err: errors.New("invalid_grant")})

	noRefresh := id()
	if err := tk.Save(ctx, noRefresh, id(), auth.IDPTokens{AccessToken: "a", Expiry: time.Now().Add(-time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if got, err := tk.Access(ctx, noRefresh); err != nil || got != "" {
		t.Fatalf("expired without refresh token = %q, %v", got, err)
	}
	if _, ok := st.rows[noRefresh.Bytes]; ok {
		t.Error("an expired token is deleted")
	}

	rejected := id()
	if err := tk.Save(ctx, rejected, id(), auth.IDPTokens{AccessToken: "a", RefreshToken: "rt", Expiry: time.Now().Add(-time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if got, err := tk.Access(ctx, rejected); err != nil || got != "" {
		t.Fatalf("refresh rejected = %q, %v", got, err)
	}

	// Still valid but close to expiry: a failed refresh keeps using it.
	soon := id()
	if err := tk.Save(ctx, soon, id(), auth.IDPTokens{AccessToken: "still-valid", RefreshToken: "rt", Expiry: time.Now().Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if got, err := tk.Access(ctx, soon); err != nil || got != "still-valid" {
		t.Fatalf("valid token after a failed refresh = %q, %v", got, err)
	}
}

// TestTokens_SignOutDuringRefresh: a refresh that finishes after a sign-out
// does not bring the tokens back.
func TestTokens_SignOutDuringRefresh(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newMem()
	user := id()
	r := &rotatingRefresher{valid: "rt-0"}
	tk := newTokens(t, st, signOutFirst{r, func() { _ = st.DeleteUserIDPToken(ctx, user) }})
	if err := tk.Save(ctx, user, id(), auth.IDPTokens{AccessToken: "old", RefreshToken: "rt-0", Expiry: time.Now().Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if _, err := tk.Access(ctx, user); err != nil {
		t.Fatal(err)
	}
	if _, ok := st.rows[user.Bytes]; ok {
		t.Error("a refresh recreated the tokens of a signed-out user")
	}
}

// signOutFirst signs the user out while the refresh is at the provider.
type signOutFirst struct {
	Refresher
	signOut func()
}

func (s signOutFirst) Refresh(ctx context.Context, p pgtype.UUID, rt string) (auth.IDPTokens, error) {
	s.signOut()
	return s.Refresher.Refresh(ctx, p, rt)
}

func TestTokens_ForgetAndEmpty(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newMem()
	tk := newTokens(t, st, nil)
	user := id()
	if err := tk.Save(ctx, user, id(), auth.IDPTokens{AccessToken: "a", Expiry: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if err := tk.Forget(ctx, user); err != nil || len(st.rows) != 0 {
		t.Fatalf("Forget = %v, rows %d", err, len(st.rows))
	}
	if err := tk.Save(ctx, user, id(), auth.IDPTokens{AccessToken: "a", Expiry: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if err := tk.Save(ctx, user, id(), auth.IDPTokens{}); err != nil || len(st.rows) != 0 {
		t.Fatalf("empty sign-in = %v, rows %d", err, len(st.rows))
	}
}

func TestIDPTokens_NeverPrinted(t *testing.T) {
	t.Parallel()
	tok := auth.IDPTokens{AccessToken: "secret-a", RefreshToken: "secret-r"}
	for _, s := range []string{fmt.Sprintf("%v", tok), fmt.Sprintf("%+v", tok), fmt.Sprintf("%#v", tok)} {
		if strings.Contains(s, "secret") {
			t.Errorf("printed a token: %s", s)
		}
	}
}
