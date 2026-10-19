package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/agentpolicy"
	"github.com/galileostudio/baleia/internal/audit"
)

type fakeRecorder struct{ events []audit.Event }

func (f *fakeRecorder) Record(_ context.Context, e audit.Event) error {
	f.events = append(f.events, e)
	return nil
}

func TestAuditHooks_OneEventPerCall(t *testing.T) {
	cases := []struct {
		name   string
		o      Outcome
		want   audit.Outcome
		reason string
	}{
		{"allowed", Outcome{Allowed: true, Rows: 3, Duration: 10 * time.Millisecond}, audit.Allowed, ""},
		{"refused", Outcome{Allowed: false, Reason: agentpolicy.ReasonPolicy}, audit.Denied, "policy"},
		{"not found", Outcome{Allowed: true, Err: errNotFound}, audit.Denied, "not_found"},
		{"invalid", Outcome{Allowed: true, Err: errInvalid}, audit.Denied, "validation"},
		{"internal", Outcome{Allowed: true, Err: errors.New("boom")}, audit.Error, "internal"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := &fakeRecorder{}
			NewAuditHooks(rec).OnToolCall(context.Background(), agentpolicy.Call{
				Tool: agentpolicy.ToolRunQuery, WorkspaceID: uid(9), SQL: stringPtr("SELECT secret"),
			}, tc.o)
			if len(rec.events) != 1 {
				t.Fatalf("events = %d, want exactly 1", len(rec.events))
			}
			ev := rec.events[0]
			if ev.Action != audit.AgentToolCall {
				t.Errorf("action = %q, want %q", ev.Action, audit.AgentToolCall)
			}
			if ev.Outcome != tc.want {
				t.Errorf("outcome = %q, want %q", ev.Outcome, tc.want)
			}
			if ev.Reason != tc.reason {
				t.Errorf("reason = %q, want %q", ev.Reason, tc.reason)
			}
			hash, _ := ev.Changes["args_sha256"].To.(string)
			if len(hash) != 64 {
				t.Errorf("args_sha256 = %q, want 64 hex chars", hash)
			}
			raw, _ := json.Marshal(ev)
			if strings.Contains(string(raw), "SELECT secret") {
				t.Errorf("audit event contains SQL text: %s", raw)
			}
		})
	}
}

func TestAuditHooks_ExecutionResource(t *testing.T) {
	rec := &fakeRecorder{}
	NewAuditHooks(rec).OnToolCall(context.Background(), agentpolicy.Call{Tool: agentpolicy.ToolGetResults, WorkspaceID: uid(9)}, Outcome{
		Allowed: true, ExecutionID: "exec-1",
	})
	if len(rec.events) != 1 {
		t.Fatal("expected one event")
	}
	ev := rec.events[0]
	if ev.ResourceType != "execution" || ev.ResourceID != "exec-1" {
		t.Fatalf("resource = %s/%s, want execution/exec-1", ev.ResourceType, ev.ResourceID)
	}
}

func stringPtr(s string) *string { return &s }
