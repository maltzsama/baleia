package mcp

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/agentpolicy"
	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/dialect"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func uid(b byte) pgtype.UUID { return pgtype.UUID{Bytes: [16]byte{b}, Valid: true} }

type fakeTokenStore struct {
	token db.AgentToken
	agent db.Agent
	owner db.User
}

func (s *fakeTokenStore) GetAgentTokenByHash(_ context.Context, hash string) (db.AgentToken, error) {
	if hash != s.token.TokenHash {
		return db.AgentToken{}, pgx.ErrNoRows
	}
	return s.token, nil
}

func (s *fakeTokenStore) GetAgentByID(context.Context, pgtype.UUID) (db.Agent, error) {
	return s.agent, nil
}

func (s *fakeTokenStore) GetUserByID(context.Context, pgtype.UUID) (db.User, error) {
	return s.owner, nil
}

func (s *fakeTokenStore) TouchAgentToken(context.Context, pgtype.UUID) error { return nil }

func fixtures() (*fakeTokenStore, string) {
	raw := auth.AgentTokenPrefix + "test-secret"
	store := &fakeTokenStore{
		token: db.AgentToken{
			ID:        uid(3),
			AgentID:   uid(1),
			TokenHash: auth.AgentTokenHash(raw),
			ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true},
		},
		agent: db.Agent{ID: uid(1), OwnerID: uid(2), Kind: "external", Status: "active"},
		owner: db.User{ID: uid(2), Status: auth.StatusActive},
	}
	return store, raw
}

func newTestServer(t *testing.T, gate *agentpolicy.Gate, origins []string) (*httptest.Server, string) {
	t.Helper()
	tokens, raw := fixtures()
	server := New(nil, gate, tokens, nil, origins)
	ts := httptest.NewServer(server.Handler())
	t.Cleanup(ts.Close)
	return ts, raw
}

func TestAuth_RequiresAgentToken(t *testing.T) {
	t.Parallel()
	ts, raw := newTestServer(t, nil, nil)
	cases := []struct {
		name    string
		headers map[string]string
		want    int
	}{
		{"missing", nil, http.StatusUnauthorized},
		{"jwt", map[string]string{"Authorization": "Bearer not-an-agent-token"}, http.StatusUnauthorized},
		{"api key", map[string]string{"X-Api-Key": "bal_test"}, http.StatusUnauthorized},
		{"valid", map[string]string{"Authorization": "Bearer " + raw}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req, _ := http.NewRequest(http.MethodPost, ts.URL, strings.NewReader(`{}`))
			for k, v := range tc.headers {
				req.Header.Set(k, v)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			_, _ = io.Copy(io.Discard, resp.Body)
			if tc.want != 0 && resp.StatusCode != tc.want {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tc.want)
			}
			if tc.want == 0 && resp.StatusCode == http.StatusUnauthorized {
				t.Fatal("valid agent token was rejected")
			}
		})
	}
}

func TestOriginRejected(t *testing.T) {
	t.Parallel()
	ts, raw := newTestServer(t, nil, []string{"https://ok.example"})
	send := func(origin string) int {
		req, _ := http.NewRequest(http.MethodPost, ts.URL, strings.NewReader(`{}`))
		req.Header.Set("Authorization", "Bearer "+raw)
		req.Header.Set("Origin", origin)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		_, _ = io.Copy(io.Discard, resp.Body)
		return resp.StatusCode
	}
	if got := send("https://evil.example"); got != http.StatusForbidden {
		t.Fatalf("foreign origin = %d, want 403", got)
	}
	if got := send("https://ok.example"); got == http.StatusForbidden {
		t.Fatalf("allowed origin was rejected")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func connect(t *testing.T, ts *httptest.Server, raw string) *mcpsdk.ClientSession {
	t.Helper()
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test", Version: "1"}, nil)
	transport := &mcpsdk.StreamableClientTransport{
		Endpoint: ts.URL,
		HTTPClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			req.Header.Set("Authorization", "Bearer "+raw)
			return http.DefaultTransport.RoundTrip(req)
		})},
		DisableStandaloneSSE: true,
	}
	session, err := client.Connect(context.Background(), transport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func TestToolsList(t *testing.T) {
	t.Parallel()
	ts, raw := newTestServer(t, nil, nil)
	session := connect(t, ts, raw)
	res, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Tools) != len(agentpolicy.AllTools) {
		t.Fatalf("tools = %d, want %d", len(res.Tools), len(agentpolicy.AllTools))
	}
	names := make(map[string]bool, len(res.Tools))
	for _, tool := range res.Tools {
		names[tool.Name] = true
	}
	for _, want := range agentpolicy.AllTools {
		if !names[want] {
			t.Errorf("missing tool %q", want)
		}
	}
}

type gateStore struct {
	agentpolicy.Store
	agent db.Agent
}

func (s gateStore) GetAgentByID(context.Context, pgtype.UUID) (db.Agent, error) {
	return s.agent, nil
}

func TestRefusalBeforeHandler(t *testing.T) {
	t.Parallel()
	tokens, raw := fixtures()
	store := gateStore{agent: db.Agent{
		ID: uid(1), OwnerID: uid(2), Kind: "external", Status: "active",
		Policy: []byte(`{"tools":[]}`),
	}}
	gate := agentpolicy.NewGate(store, nil, agentpolicy.DefaultCeilings(), dialect.NewRegistry())
	server := New(nil, gate, tokens, nil, nil)
	ts := httptest.NewServer(server.Handler())
	t.Cleanup(ts.Close)

	session := connect(t, ts, raw)
	res, err := session.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name:      agentpolicy.ToolRunQuery,
		Arguments: map[string]any{"workspace_id": "00000000-0000-0000-0000-00000000000a", "sql": "SELECT 1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("run_query was not refused")
	}
	content, _ := res.StructuredContent.(map[string]any)
	errObj, _ := content["error"].(map[string]any)
	if errObj["reason"] != "policy" {
		t.Fatalf("refusal = %#v, want reason policy", content)
	}
}

// TestImportBoundary keeps the MCP package on the in-process API surface: it
// must never reach the database, engines or object storage directly (#58).
func TestImportBoundary(t *testing.T) {
	t.Parallel()
	out, err := exec.Command("go", "list", "-f", `{{join .Imports "\n"}}`, "github.com/galileostudio/baleia/internal/mcp").Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	forbidden := []string{
		"/internal/db",
		"/internal/trino",
		"/internal/queryengine",
		"/internal/temporal",
		"/internal/catalogmanager",
		"/internal/storage",
	}
	for _, imp := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		for _, bad := range forbidden {
			if strings.HasSuffix(imp, bad) {
				t.Errorf("internal/mcp imports %s", imp)
			}
		}
	}
}
