package activities

import (
	"context"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/dialect"
	"github.com/galileostudio/baleia/internal/store/model"
	"github.com/galileostudio/baleia/internal/store/repo"
	"github.com/galileostudio/baleia/internal/testdb"
)

// TestCleanup_DeletesOnlyExpired: the daily pass removes expired refresh
// tokens, keeping revoked ones until they expire and expired or used reset tokens, and nothing else (#51).
func TestCleanup_DeletesOnlyExpired(t *testing.T) {
	pool := testdb.New(t)
	gdb := pool
	q := repo.New(gdb)
	ctx := context.Background()
	now := time.Now()

	password := "not-used-by-this-test"
	user, err := q.CreateUser(ctx, db.CreateUserParams{
		Name:         "cleanup",
		Email:        "cleanup@example.test",
		Role:         "member",
		AuthProvider: "local",
		PasswordHash: &password,
	})
	if err != nil {
		t.Fatal(err)
	}

	family := db.UUID{Bytes: [16]byte{1}, Valid: true}
	insertRefresh := func(hash string, expires time.Time, revoked bool) {
		rt, err := q.CreateRefreshToken(ctx, db.CreateRefreshTokenParams{
			UserID:    user.ID,
			TokenHash: hash,
			ExpiresAt: db.Timestamptz{Time: expires, Valid: true},
			FamilyID:  family,
		})
		if err != nil {
			t.Fatal(err)
		}
		if revoked {
			if err := q.RevokeRefreshToken(ctx, rt.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
	insertRefresh("expired", now.Add(-time.Hour), false)
	insertRefresh("revoked", now.Add(time.Hour), true)
	insertRefresh("live", now.Add(time.Hour), false)

	insertReset := func(hash string, expires time.Time) {
		if err := q.CreatePasswordResetToken(ctx, db.CreatePasswordResetTokenParams{
			TokenHash: hash,
			UserID:    user.ID,
			ExpiresAt: db.Timestamptz{Time: expires, Valid: true},
		}); err != nil {
			t.Fatal(err)
		}
	}
	insertReset("reset-expired", now.Add(-time.Hour))
	insertReset("reset-used", now.Add(time.Hour))
	insertReset("reset-live", now.Add(time.Hour))
	if _, err := q.ConsumePasswordResetToken(ctx, "reset-used"); err != nil {
		t.Fatal(err)
	}

	provider, err := q.CreateAuthProvider(ctx, db.CreateAuthProviderParams{
		Name: "oidc", Type: "oidc", ConfigEnc: "{}", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	insertAttempt := func(state string, expires time.Time) {
		if err := q.CreateOIDCLoginAttempt(ctx, db.CreateOIDCLoginAttemptParams{
			State:        state,
			ProviderID:   provider.ID,
			CodeVerifier: "verifier",
			ExpiresAt:    db.Timestamptz{Time: expires, Valid: true},
		}); err != nil {
			t.Fatal(err)
		}
	}
	insertAttempt("oidc-expired", now.Add(-time.Hour))
	insertAttempt("oidc-live", now.Add(time.Hour))

	old := defaultActivities
	defaultActivities = &Activities{dialects: dialect.NewRegistry(), store: q}
	t.Cleanup(func() { defaultActivities = old })

	if err := CleanupExpiredTokens(ctx); err != nil {
		t.Fatal(err)
	}

	count := func(table, hash string) int64 {
		var n int64
		if err := gdb.Table(table).Where("token_hash = ?", hash).Count(&n).Error; err != nil {
			t.Fatal(err)
		}
		return n
	}
	for _, tc := range []struct {
		table string
		hash  string
		want  int64
	}{
		{"refresh_tokens", "live", 1},
		{"refresh_tokens", "expired", 0},
		// A revoked token is kept until it expires, so a replay is still
		// detected (#1031).
		{"refresh_tokens", "revoked", 1},
		{"password_reset_tokens", "reset-live", 1},
		{"password_reset_tokens", "reset-expired", 0},
		{"password_reset_tokens", "reset-used", 0},
	} {
		if got := count(tc.table, tc.hash); got != tc.want {
			t.Errorf("%s %q: %d rows, want %d", tc.table, tc.hash, got, tc.want)
		}
	}
	for state, want := range map[string]int64{"oidc-live": 1, "oidc-expired": 0} {
		var n int64
		if err := gdb.Model(&model.OIDCLoginAttempt{}).Where("state = ?", state).Count(&n).Error; err != nil {
			t.Fatal(err)
		}
		if n != want {
			t.Errorf("oidc attempt %q: %d rows, want %d", state, n, want)
		}
	}
}
