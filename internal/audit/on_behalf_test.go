package audit

import (
	"context"
	"log/slog"
	"testing"

	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/store/model"
	"github.com/galileostudio/baleia/internal/store/repo"
	"github.com/galileostudio/baleia/internal/testdb"
	"github.com/google/uuid"
)

// TestOnBehalfOfOnlyForAgents: agent events carry actor_kind=agent, the agent
// id and the owner in on_behalf_of; human events leave on_behalf_of NULL (#61).
func TestOnBehalfOfOnlyForAgents(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	gdb := pool
	q := repo.New(gdb)
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

	var agentEvent model.AuditEvent
	if err := gdb.Where("action = ?", "agent.tool_call").Order("id DESC").First(&agentEvent).Error; err != nil {
		t.Fatal(err)
	}
	kind := agentEvent.ActorKind
	actorID := string(*agentEvent.ActorID)
	method := *agentEvent.AuthMethod
	onBehalf := agentEvent.OnBehalfOf
	if kind != "agent" {
		t.Errorf("actor_kind = %q, want agent", kind)
	}
	if actorID != agent.String() {
		t.Errorf("actor_id = %q, want %q", actorID, agent.String())
	}
	if method != AuthAgentToken {
		t.Errorf("auth_method = %q, want %q", method, AuthAgentToken)
	}
	if onBehalf == nil || string(*onBehalf) != owner.String() {
		t.Errorf("on_behalf_of = %v, want %q", onBehalf, owner.String())
	}

	var humanEvent model.AuditEvent
	if err := gdb.Where("action = ?", "catalog.update").Order("id DESC").First(&humanEvent).Error; err != nil {
		t.Fatal(err)
	}
	kind = humanEvent.ActorKind
	onBehalf = humanEvent.OnBehalfOf
	if kind != "user" {
		t.Errorf("human actor_kind = %q, want user", kind)
	}
	if onBehalf != nil {
		t.Errorf("human on_behalf_of = %v, want NULL", onBehalf)
	}
}

// TestOnBehalfOfPropagatesToPartitions: the column is on the partitioned parent
// and therefore on the existing partitions.
