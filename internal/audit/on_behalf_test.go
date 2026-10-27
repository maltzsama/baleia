package audit

import (
	"context"
	"log/slog"
	"testing"

	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/testdb"
	"github.com/google/uuid"
)

// TestOnBehalfOfOnlyForAgents: agent events carry actor_kind=agent, the agent
// id and the owner in on_behalf_of; human events leave on_behalf_of NULL (#61).
func TestOnBehalfOfOnlyForAgents(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	q := db.New(pool)
	rec := NewPostgres(slog.New(slog.DiscardHandler), q, nil, nil)
	owner := uuid.New()
	agent := uuid.New()

	agentCtx := auth.WithAgentPrincipal(context.Background(), owner.String(), agent.String())
	agentCtx = WithAuthMethod(agentCtx, AuthAgentToken)
	if err := rec.Record(agentCtx, Event{Action: AgentToolCall, ResourceType: "workspace", Outcome: Allowed}); err != nil {
		t.Fatal(err)
	}

	humanCtx := context.WithValue(context.Background(), auth.ContextKeyUserID, owner.String())
	if err := rec.Record(humanCtx, Event{Action: CatalogUpdate, ResourceType: "catalog", Outcome: Allowed}); err != nil {
		t.Fatal(err)
	}

	var kind, actorID, method string
	var onBehalf *string
	row := pool.QueryRow(context.Background(),
		`SELECT actor_kind, actor_id, auth_method, on_behalf_of::text
		   FROM audit_events WHERE action = 'agent.tool_call' ORDER BY id DESC LIMIT 1`)
	if err := row.Scan(&kind, &actorID, &method, &onBehalf); err != nil {
		t.Fatal(err)
	}
	if kind != "agent" {
		t.Errorf("actor_kind = %q, want agent", kind)
	}
	if actorID != agent.String() {
		t.Errorf("actor_id = %q, want %q", actorID, agent.String())
	}
	if method != AuthAgentToken {
		t.Errorf("auth_method = %q, want %q", method, AuthAgentToken)
	}
	if onBehalf == nil || *onBehalf != owner.String() {
		t.Errorf("on_behalf_of = %v, want %q", onBehalf, owner.String())
	}

	row = pool.QueryRow(context.Background(),
		`SELECT actor_kind, on_behalf_of::text
		   FROM audit_events WHERE action = 'catalog.update' ORDER BY id DESC LIMIT 1`)
	if err := row.Scan(&kind, &onBehalf); err != nil {
		t.Fatal(err)
	}
	if kind != "user" {
		t.Errorf("human actor_kind = %q, want user", kind)
	}
	if onBehalf != nil {
		t.Errorf("human on_behalf_of = %v, want NULL", onBehalf)
	}
}

// TestOnBehalfOfPropagatesToPartitions: the column is on the partitioned parent
// and therefore on the existing partitions.
func TestOnBehalfOfPropagatesToPartitions(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM information_schema.columns
		  WHERE table_name LIKE 'audit_events\_%' ESCAPE '\' AND column_name = 'on_behalf_of'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("no audit_events partition has on_behalf_of")
	}
}
