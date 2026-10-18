package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestAgentTokenRejectedOnRESTAPI: an agent token is valid only on /mcp. Every
// REST surface answers 401 agent_token_not_allowed and never runs the handler
// (#59).
func TestAgentTokenRejectedOnRESTAPI(t *testing.T) {
	handler := func(ctx context.Context, w http.ResponseWriter, _ *http.Request, _ interface{}) (interface{}, error) {
		t.Fatal("handler must not be called with an agent token")
		return nil, nil
	}
	for _, path := range []string{"/api/v1/workspaces", "/api/v1/agents", "/api/v1/auth/me"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer bal_agt_deadbeef")
		rec := httptest.NewRecorder()
		if _, err := authMiddlewareFunc("test-secret", nil)(handler, "ListWorkspaces")(context.Background(), rec, req, nil); err != nil {
			t.Fatal(err)
		}
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s: status = %d, want 401", path, rec.Code)
		}
		if body := rec.Body.String(); !strings.Contains(body, "agent_token_not_allowed") {
			t.Errorf("%s: body = %q, want agent_token_not_allowed", path, body)
		}
	}
}

func TestAgentTokenRejectedOnChiRoutes(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler must not be called with an agent token")
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/lens/overview", nil)
	req.Header.Set("Authorization", "Bearer bal_agt_deadbeef")
	rec := httptest.NewRecorder()
	chiAuthMiddleware("test-secret", nil)(next).ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if body := rec.Body.String(); !strings.Contains(body, "agent_token_not_allowed") {
		t.Errorf("body = %q, want agent_token_not_allowed", body)
	}
}
