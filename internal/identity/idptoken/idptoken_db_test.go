package idptoken

import (
	"context"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/testdb"
	"github.com/jackc/pgx/v5"
)

// TestTokens_Postgres runs the token store on the real schema: the sign-in
// save is conditional on an active account and an enabled provider, a
// refresh writes over the row it locked, and a removed provider takes its
// tokens with it.
func TestTokens_Postgres(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := testdb.New(t)
	q := db.New(pool)
	tx := func(ctx context.Context, fn func(Store) error) error {
		return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error { return fn(db.New(tx)) })
	}
	r := &rotatingRefresher{valid: "rt-0"}
	tk := New(q, tx, keyring(t), r, nil)

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
