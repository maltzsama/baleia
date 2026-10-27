package assistant

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/assistant/llm"
	adb "github.com/galileostudio/baleia/internal/db/assistant"
	"github.com/galileostudio/baleia/internal/testdb"
	"github.com/jackc/pgx/v5/pgtype"
)

type fakeTools struct {
	calls  []string
	result any
}

func (f *fakeTools) CallTool(_ context.Context, name string, _ map[string]any) (any, error) {
	f.calls = append(f.calls, name)
	return f.result, nil
}

func newTurnFixture(t *testing.T) (*Service, pgtype.UUID, pgtype.UUID) {
	t.Helper()
	pool := testdb.New(t)
	svc := New(adb.New(pool), nil)
	ctx := context.Background()
	var userID, workspaceID pgtype.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (name, email, role, password_hash) VALUES ('a', 'a@turn.test', 'member', 'x') RETURNING id`,
	).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO workspaces (name, type, polaris_catalog_name, owner_user_id)
		 VALUES ('turn-ws', 'personal', 'turn_polaris', $1) RETURNING id`, userID,
	).Scan(&workspaceID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpdateSettings(ctx, SettingsInput{
		Enabled: true, Provider: strptr("anthropic"), Model: strptr("fake"),
		DataSharing: "metadata_and_samples", MaxTurnSeconds: 10,
		MaxToolCallsPerTurn: 5, MaxOutputTokensPerTurn: 100, DailyTokenBudgetPerUser: 100000,
	}); err != nil {
		t.Fatal(err)
	}
	return svc, userID, workspaceID
}

func waitIdle(t *testing.T, svc *Service, userID, id pgtype.UUID) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		conv, err := svc.GetConversation(context.Background(), userID, id)
		if err != nil {
			t.Fatal(err)
		}
		if conv.State == "idle" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("turn did not finish")
}

// TestTurnToolCallsGoThroughTheCaller: the model's tool call is persisted,
// executed through the injected caller (the in-memory MCP client in production)
// and returned to the model; the provider sees no rows under metadata_only.
func TestTurnToolCallsGoThroughTheCaller(t *testing.T) {
	t.Parallel()
	svc, userID, workspaceID := newTurnFixture(t)
	conv, err := svc.CreateConversation(context.Background(), userID, workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	provider := &llm.Fake{Responses: []llm.Response{
		{ToolCalls: []llm.ToolCall{{ID: "c1", Name: "run_query", Arguments: json.RawMessage(`{"sql":"select 1"}`)}}},
		{Text: "done", InputTokens: 1, OutputTokens: 1},
	}}
	tools := &fakeTools{result: map[string]any{"rows": []any{1, 2}, "row_count": 2}}
	runner := NewRunner(svc, provider, tools)

	seq, err := runner.Start(context.Background(), userID, conv.ID, "how many?")
	if err != nil {
		t.Fatal(err)
	}
	if seq != 1 {
		t.Fatalf("user seq = %d, want 1", seq)
	}
	waitIdle(t, svc, userID, conv.ID)

	if len(tools.calls) != 1 || tools.calls[0] != "run_query" {
		t.Fatalf("tool calls = %v", tools.calls)
	}
	msgs, err := svc.ListMessages(context.Background(), conv.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	roles := make([]string, 0, len(msgs))
	for _, m := range msgs {
		roles = append(roles, m.Role)
	}
	want := []string{"user", "tool_call", "tool_result", "assistant"}
	if len(roles) != len(want) {
		t.Fatalf("roles = %v, want %v", roles, want)
	}
	for i := range want {
		if roles[i] != want[i] {
			t.Fatalf("roles = %v, want %v", roles, want)
		}
	}
	// The model must see the tool result in a later request.
	if len(provider.Requests) < 2 {
		t.Fatalf("provider calls = %d, want 2", len(provider.Requests))
	}
	last := provider.Requests[len(provider.Requests)-1]
	found := false
	for _, msg := range last.Messages {
		if msg.Role == "tool" && msg.ToolCallID == "c1" {
			found = true
		}
	}
	if !found {
		t.Fatal("the second provider request has no tool result")
	}
}

// TestTurnBudgetStopsBeforeTheProvider: an exhausted budget persists a notice
// and never calls the model.
func TestTurnBudgetStopsBeforeTheProvider(t *testing.T) {
	t.Parallel()
	svc, userID, workspaceID := newTurnFixture(t)
	if _, err := svc.UpdateSettings(context.Background(), SettingsInput{
		Enabled: true, Provider: strptr("anthropic"), Model: strptr("fake"),
		DataSharing: "metadata_only", MaxTurnSeconds: 10,
		MaxToolCallsPerTurn: 5, MaxOutputTokensPerTurn: 100, DailyTokenBudgetPerUser: 0,
	}); err != nil {
		t.Fatal(err)
	}
	// 0 disables the check, so force a tiny budget with a large usage row.
	if _, err := svc.UpdateSettings(context.Background(), SettingsInput{
		Enabled: true, Provider: strptr("anthropic"), Model: strptr("fake"),
		DataSharing: "metadata_only", MaxTurnSeconds: 10,
		MaxToolCallsPerTurn: 5, MaxOutputTokensPerTurn: 100, DailyTokenBudgetPerUser: 1,
	}); err != nil {
		t.Fatal(err)
	}
	conv, err := svc.CreateConversation(context.Background(), userID, workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	input := int32(10)
	if _, err := svc.AppendMessage(context.Background(), conv.ID, "assistant", map[string]string{"text": "old"}, &input, &input); err != nil {
		t.Fatal(err)
	}
	provider := &llm.Fake{Responses: []llm.Response{{Text: "should not run"}}}
	runner := NewRunner(svc, provider, &fakeTools{})
	if _, err := runner.Start(context.Background(), userID, conv.ID, "hello"); err != nil {
		t.Fatal(err)
	}
	waitIdle(t, svc, userID, conv.ID)
	if len(provider.Requests) != 0 {
		t.Fatalf("provider was called %d times, want 0", len(provider.Requests))
	}
	msgs, err := svc.ListMessages(context.Background(), conv.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	last := msgs[len(msgs)-1]
	if last.Role != "notice" {
		t.Fatalf("last role = %s, want notice", last.Role)
	}
}

// TestRunnerSlot: an empty or nil slot loads nil; a stored runner loads back.
func TestRunnerSlot(t *testing.T) {
	t.Parallel()
	var none *RunnerSlot
	if none.Load() != nil {
		t.Fatal("a nil slot must load nil")
	}
	slot := &RunnerSlot{}
	if slot.Load() != nil {
		t.Fatal("an empty slot must load nil")
	}
	r := &Runner{}
	slot.Store(r)
	if slot.Load() != r {
		t.Fatal("the stored runner must load back")
	}
}
