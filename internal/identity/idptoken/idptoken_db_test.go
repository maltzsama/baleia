package idptoken_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/crypto"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/identity/idptoken"
	"github.com/galileostudio/baleia/internal/store/repo"
	"github.com/galileostudio/baleia/internal/testdb"
	"github.com/jackc/pgx/v5/pgtype"
)

// TestTokens_Postgres runs the token store on the real schema: the sign-in
// save is conditional on an active account and an enabled provider, a
// refresh writes over the row it locked, and a removed provider takes its
// tokens with it.
func TestTokens_Postgres(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testdb.New(t)
	q := repo.New(testdb.GORM(t, pool))
	tx := q.TxIDPToken
	r := &dbRotatingRefresher{valid: "rt-0"}
	tk := idptoken.New(q, tx, dbTestKeyring(t), r, nil)

	ap, err := q.CreateAuthProvider(ctx, db.CreateAuthProviderParams{Name: "corp", Type: "oidc", ConfigEnc: "x", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	newUser := func(email string) db.User {
		ext := email
		u, err := q.CreateUser(ctx, db.CreateUserParams{Name: email, Email: email, Role: "member", AuthProvider: "oidc", AuthProviderID: ap.ID, ExternalID: &ext})
		if err != nil {
			t.Fatal(err)
		}
		return u
	}
	alice := newUser("alice@corp.test")
	soon := auth.IDPTokens{AccessToken: "first", RefreshToken: "rt-0", Expiry: time.Now().Add(time.Minute)}
	if err := tk.Save(ctx, alice.ID, ap.ID, soon); err != nil {
		t.Fatal(err)
	}
	if got, err := tk.Access(ctx, alice.ID); err != nil || got != "access-1" {
		t.Fatalf("refresh on Postgres = %q, %v", got, err)
	}
	if got, err := tk.Access(ctx, alice.ID); err != nil || got != "access-1" || r.calls.Load() != 1 {
		t.Fatalf("fresh token = %q, %v, refreshes %d", got, err, r.calls.Load())
	}

	bob := newUser("bob@corp.test")
	if _, err := pool.Exec(ctx, "UPDATE users SET status = 'suspended' WHERE id = $1", bob.ID); err != nil {
		t.Fatal(err)
	}
	if err := tk.Save(ctx, bob.ID, ap.ID, soon); err != nil {
		t.Fatal(err)
	}
	if got, _ := tk.Access(ctx, bob.ID); got != "" {
		t.Error("a suspended account kept identity-provider tokens")
	}

	if _, err := pool.Exec(ctx, "UPDATE auth_providers SET enabled = false WHERE id = $1", ap.ID); err != nil {
		t.Fatal(err)
	}
	carol := newUser("carol@corp.test")
	if err := tk.Save(ctx, carol.ID, ap.ID, soon); err != nil {
		t.Fatal(err)
	}
	if got, _ := tk.Access(ctx, carol.ID); got != "" {
		t.Error("a disabled provider's sign-in kept identity-provider tokens")
	}
}

// dbRotatingRefresher accepts each refresh token once and issues the next, as
// a provider with refresh-token rotation does.
type dbRotatingRefresher struct {
	mu    sync.Mutex
	valid string
	n     int
	calls atomic.Int32
	err   error
}

func (r *dbRotatingRefresher) Refresh(_ context.Context, _ pgtype.UUID, rt string) (auth.IDPTokens, error) {
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

func dbTestKeyring(t *testing.T) *crypto.Keyring {
	t.Helper()
	kr, err := crypto.NewKeyring("test-secret-0123456789abcdef-0123456789", "")
	if err != nil {
		t.Fatal(err)
	}
	return kr
}
