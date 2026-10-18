package api

import (
	"context"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/testdb"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestSafeRelativePath(t *testing.T) {
	cases := map[string]bool{
		"/sql":              true,
		"/sql?tab=1":        true,
		"":                  false,
		"//evil.example":    false,
		"/\\evil.example":   false,
		"https://evil.test": false,
		"/a\nb":             false,
		"/a\tb":             false,
	}
	for next, want := range cases {
		if got := safeRelativePath(next); got != want {
			t.Errorf("safeRelativePath(%q) = %v, want %v", next, got, want)
		}
	}
}

func oidcAttemptFixture(t *testing.T) (*db.Queries, db.AuthProvider) {
	t.Helper()
	pool := testdb.New(t)
	q := db.New(pool)
	provider, err := q.CreateAuthProvider(context.Background(), db.CreateAuthProviderParams{
		Name:      "test",
		Type:      "oidc",
		ConfigEnc: "{}",
		Enabled:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return q, provider
}

// TestOIDC_AttemptSingleUse: consuming a state deletes the row, so a replayed
// callback finds nothing (#250).
func TestOIDC_AttemptSingleUse(t *testing.T) {
	q, provider := oidcAttemptFixture(t)
	ctx := context.Background()
	const state = "state-1"
	if err := q.CreateOIDCLoginAttempt(ctx, db.CreateOIDCLoginAttemptParams{
		State:        state,
		ProviderID:   provider.ID,
		CodeVerifier: "verifier-1",
		ExpiresAt:    pgtype.Timestamptz{Time: time.Now().Add(time.Minute), Valid: true},
	}); err != nil {
		t.Fatal(err)
	}

	first, err := q.ConsumeOIDCLoginAttempt(ctx, state)
	if err != nil {
		t.Fatal(err)
	}
	if first.CodeVerifier != "verifier-1" {
		t.Errorf("verifier = %q, want verifier-1", first.CodeVerifier)
	}
	if _, err := q.ConsumeOIDCLoginAttempt(ctx, state); err == nil {
		t.Fatal("a replayed state was consumed twice")
	}
}

// TestOIDC_AttemptExpired: an attempt older than its TTL is refused (#250).
func TestOIDC_AttemptExpired(t *testing.T) {
	q, provider := oidcAttemptFixture(t)
	ctx := context.Background()
	if err := q.CreateOIDCLoginAttempt(ctx, db.CreateOIDCLoginAttemptParams{
		State:        "state-expired",
		ProviderID:   provider.ID,
		CodeVerifier: "verifier-1",
		ExpiresAt:    pgtype.Timestamptz{Time: time.Now().Add(-time.Minute), Valid: true},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := q.ConsumeOIDCLoginAttempt(ctx, "state-expired"); err == nil {
		t.Fatal("an expired attempt was consumed")
	}
}
