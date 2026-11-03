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

type memStore struct {
	mu   sync.Mutex
	rows map[[16]byte]db.UserIdpToken
}

func (m *memStore) UpsertUserIDPToken(_ context.Context, a db.UpsertUserIDPTokenParams) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rows[a.UserID.Bytes] = db.UserIdpToken{UserID: a.UserID, AuthProviderID: a.AuthProviderID, AccessTokenEnc: a.AccessTokenEnc, RefreshTokenEnc: a.RefreshTokenEnc, ExpiresAt: a.ExpiresAt}
	return nil
}

func (m *memStore) GetUserIDPToken(_ context.Context, id pgtype.UUID) (db.UserIdpToken, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.rows[id.Bytes]
	if !ok {
		return db.UserIdpToken{}, pgx.ErrNoRows
	}
	return r, nil
}

func (m *memStore) DeleteUserIDPToken(_ context.Context, id pgtype.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.rows, id.Bytes)
	return nil
}

type fakeRefresher struct {
	calls atomic.Int32
	err   error
}

func (f *fakeRefresher) Refresh(_ context.Context, _ pgtype.UUID, rt string) (auth.IDPTokens, error) {
	f.calls.Add(1)
	if f.err != nil {
		return auth.IDPTokens{}, f.err
	}
	return auth.IDPTokens{AccessToken: "new-access", RefreshToken: rt + "-rotated", Expiry: time.Now().Add(time.Hour)}, nil
}

func newTokens(t *testing.T, r Refresher) (*Tokens, *memStore) {
	t.Helper()
	kr, err := crypto.NewKeyring("test-secret-0123456789abcdef-0123456789", "")
	if err != nil {
		t.Fatal(err)
	}
	st := &memStore{rows: map[[16]byte]db.UserIdpToken{}}
	return New(st, kr, r, nil), st
}

func id() pgtype.UUID { return pgtype.UUID{Bytes: uuid.New(), Valid: true} }

func TestTokens_SaveSealsAndReturnsFresh(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	tk, st := newTokens(t, &fakeRefresher{})
	user, prov := id(), id()
	if err := tk.Save(ctx, user, prov, auth.IDPTokens{AccessToken: "secret-access", RefreshToken: "secret-refresh", Expiry: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	row := st.rows[user.Bytes]
	if strings.Contains(row.AccessTokenEnc, "secret-access") || row.RefreshTokenEnc == nil || strings.Contains(*row.RefreshTokenEnc, "secret-refresh") {
		t.Fatal("tokens must be stored sealed")
	}
	got, err := tk.Access(ctx, user)
	if err != nil || got != "secret-access" {
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

func TestTokens_RefreshesBeforeExpiry(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := &fakeRefresher{}
	tk, _ := newTokens(t, r)
	user := id()
	if err := tk.Save(ctx, user, id(), auth.IDPTokens{AccessToken: "old", RefreshToken: "rt", Expiry: time.Now().Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if got, err := tk.Access(ctx, user); err != nil || got != "new-access" {
				t.Errorf("Access = %q, %v", got, err)
			}
		})
	}
	wg.Wait()
	if n := r.calls.Load(); n != 1 {
		t.Errorf("refreshed %d times, want once for concurrent requests", n)
	}
}

func TestTokens_ExpiredWithoutRefreshIsDropped(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := &fakeRefresher{err: errors.New("invalid_grant")}
	tk, st := newTokens(t, r)

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

func TestTokens_ForgetAndEmpty(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	tk, st := newTokens(t, nil)
	user := id()
	if err := tk.Save(ctx, user, id(), auth.IDPTokens{AccessToken: "a", Expiry: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if err := tk.Forget(ctx, user); err != nil || len(st.rows) != 0 {
		t.Fatalf("Forget = %v, rows %d", err, len(st.rows))
	}
	// A sign-in without an access token clears the previous one.
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
	for _, s := range []string{fmtV(tok), fmtPlusV(tok), fmtSharpV(tok)} {
		if strings.Contains(s, "secret") {
			t.Errorf("printed a token: %s", s)
		}
	}
}

func fmtV(v any) string      { return fmt.Sprintf("%v", v) }
func fmtPlusV(v any) string  { return fmt.Sprintf("%+v", v) }
func fmtSharpV(v any) string { return fmt.Sprintf("%#v", v) }
