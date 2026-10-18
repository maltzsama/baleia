package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/audit"
	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/testdb"
	"github.com/jackc/pgx/v5/pgtype"
)

func apiKeyFixture(t *testing.T, expires *time.Time) (*db.Queries, string, db.User, db.ApiKey) {
	t.Helper()
	pool := testdb.New(t)
	q := db.New(pool)
	ctx := context.Background()
	password := "not-used-by-api-keys"
	user, err := q.CreateUser(ctx, db.CreateUserParams{
		Name:         "API Key User",
		Email:        "api-key@example.test",
		PasswordHash: &password,
		Role:         "member",
		AuthProvider: "local",
	})
	if err != nil {
		t.Fatal(err)
	}
	const secret = "bal_test_secret"
	var expiresAt pgtype.Timestamptz
	if expires != nil {
		expiresAt = pgtype.Timestamptz{Time: *expires, Valid: true}
	}
	key, err := q.CreateAPIKey(ctx, db.CreateAPIKeyParams{
		UserID:    user.ID,
		Name:      "test key",
		KeyHash:   auth.HashAPIKey(secret),
		Prefix:    secret[:8],
		Scopes:    []string{"*"},
		ExpiresAt: expiresAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	return q, secret, user, key
}

func runAuth(t *testing.T, q db.Querier, method, path, operation, apiKey string) (*httptest.ResponseRecorder, context.Context, bool) {
	t.Helper()
	var got context.Context
	called := false
	handler := func(ctx context.Context, w http.ResponseWriter, _ *http.Request, _ interface{}) (interface{}, error) {
		called = true
		got = ctx
		w.WriteHeader(http.StatusOK)
		return nil, nil
	}
	req := httptest.NewRequest(method, path, nil)
	if apiKey != "" {
		req.Header.Set("X-Api-Key", apiKey)
	}
	rec := httptest.NewRecorder()
	if _, err := authMiddlewareFunc("test-secret", q)(handler, operation)(context.Background(), rec, req, nil); err != nil {
		t.Fatal(err)
	}
	return rec, got, called
}

func TestAPIKey_Authenticates(t *testing.T) {
	q, secret, user, key := apiKeyFixture(t, nil)

	rec, ctx, called := runAuth(t, q, http.MethodGet, "/api/v1/workspaces", "ListWorkspaces", secret)
	if rec.Code != http.StatusOK || !called {
		t.Fatalf("status=%d called=%v, want 200", rec.Code, called)
	}
	if got := auth.UserIDFromContext(ctx); got != user.ID.String() {
		t.Errorf("user = %q, want %q", got, user.ID.String())
	}
	if got := auth.RoleFromContext(ctx); got != "member" {
		t.Errorf("role = %q, want member", got)
	}
	if _, _, method := audit.ActorOf(ctx); method != audit.AuthAPIKey {
		t.Errorf("audit method = %q, want %q", method, audit.AuthAPIKey)
	}

	// last_used_at is touched on a successful authentication.
	updated, err := q.GetAPIKeyByID(context.Background(), db.GetAPIKeyByIDParams{ID: key.ID, UserID: user.ID})
	if err != nil {
		t.Fatal(err)
	}
	if !updated.LastUsedAt.Valid {
		t.Error("last_used_at was not updated")
	}
}

func TestAPIKey_CannotMintKeys(t *testing.T) {
	q, secret, _, _ := apiKeyFixture(t, nil)

	for _, tc := range []struct{ method, path, operation string }{
		{http.MethodPost, "/api/v1/api-keys", "CreateApiKey"},
		{http.MethodGet, "/api/v1/api-keys", "ListApiKeys"},
		{http.MethodGet, "/api/v1/auth/me", "GetMe"},
		{http.MethodPatch, "/api/v1/me/password", "UpdatePassword"},
	} {
		rec, _, called := runAuth(t, q, tc.method, tc.path, tc.operation, secret)
		if rec.Code != http.StatusForbidden || called {
			t.Errorf("%s %s: status=%d called=%v, want 403", tc.method, tc.path, rec.Code, called)
		}
	}
}

func TestAPIKey_Expired(t *testing.T) {
	past := time.Now().Add(-time.Minute)
	q, secret, _, _ := apiKeyFixture(t, &past)

	rec, _, called := runAuth(t, q, http.MethodGet, "/api/v1/workspaces", "ListWorkspaces", secret)
	if rec.Code != http.StatusUnauthorized || called {
		t.Fatalf("status=%d called=%v, want 401", rec.Code, called)
	}
}

func TestAPIKey_Invalid(t *testing.T) {
	q, _, _, _ := apiKeyFixture(t, nil)

	rec, _, called := runAuth(t, q, http.MethodGet, "/api/v1/workspaces", "ListWorkspaces", "bal_wrong")
	if rec.Code != http.StatusUnauthorized || called {
		t.Fatalf("status=%d called=%v, want 401", rec.Code, called)
	}
}

// TestSuspendedCannotUseAPIKey: the middleware applies the status gate, so a
// suspended account cannot authenticate with a surviving key (#283).
func TestSuspendedCannotUseAPIKey(t *testing.T) {
	q, secret, user, _ := apiKeyFixture(t, nil)
	reason := "admin"
	if _, err := q.SuspendUser(context.Background(), db.SuspendUserParams{ID: user.ID, SuspendedReason: &reason}); err != nil {
		t.Fatal(err)
	}

	rec, _, called := runAuth(t, q, http.MethodGet, "/api/v1/workspaces", "ListWorkspaces", secret)
	if rec.Code != http.StatusUnauthorized || called {
		t.Fatalf("status=%d called=%v, want 401", rec.Code, called)
	}
}
