package api

import (
	"context"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/config"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/temporal/workflows"
	"github.com/galileostudio/baleia/internal/testdb"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

type agentsFixture struct {
	pool    *pgxpool.Pool
	h       *Handlers
	owner   context.Context
	other   context.Context
	admin   context.Context
	ownerID pgtype.UUID
	wsID    pgtype.UUID
}

func newAgentsFixture(t *testing.T, enabled bool) *agentsFixture {
	t.Helper()
	pool := testdb.New(t)
	ctx := context.Background()
	f := &agentsFixture{pool: pool}

	user := func(email, role string) pgtype.UUID {
		var id pgtype.UUID
		if err := pool.QueryRow(ctx,
			`INSERT INTO users (name, email, role, password_hash, personal_workspace_id)
			 VALUES ($1, $1, $2, 'x', NULL) RETURNING id`,
			email, role).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	f.ownerID = user("owner@agents.test", "member")
	otherID := user("other@agents.test", "member")
	adminID := user("admin@agents.test", "admin")
	f.owner = ctxAs(idString(f.ownerID), "member")
	f.other = ctxAs(idString(otherID), "member")
	f.admin = ctxAs(idString(adminID), "admin")

	if err := pool.QueryRow(ctx,
		`INSERT INTO workspaces (name, type, polaris_catalog_name, owner_user_id)
		 VALUES ('agents-ws', 'personal', 'agents_polaris', $1) RETURNING id`, f.ownerID).Scan(&f.wsID); err != nil {
		t.Fatal(err)
	}

	f.h = &Handlers{
		queries: db.New(pool),
		pool:    pool,
		cfg:     &config.Config{AgentsEnabled: enabled},
	}
	return f
}

func (f *agentsFixture) createAgent(t *testing.T, ctx context.Context, name string) Agent {
	t.Helper()
	got, err := f.h.CreateAgent(ctx, CreateAgentRequestObject{Body: &CreateAgentJSONRequestBody{
		Name:         name,
		WorkspaceIds: &[]openapi_types.UUID{openapi_types.UUID(f.wsID.Bytes)},
	}})
	if err != nil {
		t.Fatal(err)
	}
	resp, ok := got.(CreateAgent201JSONResponse)
	if !ok {
		t.Fatalf("create agent = %T", got)
	}
	return Agent(resp)
}

func TestAgentsFeatureFlagOff(t *testing.T) {
	f := newAgentsFixture(t, false)
	got, err := f.h.ListAgents(f.owner, ListAgentsRequestObject{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got.(ListAgents404JSONResponse); !ok {
		t.Fatalf("ListAgents with the flag off = %T, want 404", got)
	}
}

// TestAgentTokenShownOnce: the secret appears in the create response and never
// in the list (#59).
func TestAgentTokenShownOnce(t *testing.T) {
	f := newAgentsFixture(t, true)
	agent := f.createAgent(t, f.owner, "etl-agent")

	got, err := f.h.CreateAgentToken(f.owner, CreateAgentTokenRequestObject{
		AgentId: openapi_types.UUID(agent.Id),
		Body:    &CreateAgentTokenJSONRequestBody{Name: "laptop"},
	})
	if err != nil {
		t.Fatal(err)
	}
	created, ok := got.(CreateAgentToken201JSONResponse)
	if !ok {
		t.Fatalf("create token = %T", got)
	}
	if created.Token == "" || len(created.Token) <= len("bal_agt_") {
		t.Fatalf("token = %q, want a bal_agt_ secret", created.Token)
	}
	if created.Prefix == "" {
		t.Error("prefix is empty")
	}

	list, err := f.h.ListAgentTokens(f.owner, ListAgentTokensRequestObject{AgentId: openapi_types.UUID(agent.Id)})
	if err != nil {
		t.Fatal(err)
	}
	listed, ok := list.(ListAgentTokens200JSONResponse)
	if !ok {
		t.Fatalf("list tokens = %T", list)
	}
	if len(listed) != 1 {
		t.Fatalf("tokens = %d, want 1", len(listed))
	}
	if listed[0].Prefix != created.Prefix {
		t.Errorf("prefix = %q, want %q", listed[0].Prefix, created.Prefix)
	}
}

func TestAgentTokenMaxTTL(t *testing.T) {
	f := newAgentsFixture(t, true)
	agent := f.createAgent(t, f.owner, "etl-agent")

	tooLong := 365
	got, err := f.h.CreateAgentToken(f.owner, CreateAgentTokenRequestObject{
		AgentId: openapi_types.UUID(agent.Id),
		Body:    &CreateAgentTokenJSONRequestBody{Name: "too-long", ExpiresInDays: &tooLong},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got.(CreateAgentToken422JSONResponse); !ok {
		t.Fatalf("expires_in_days=365 = %T, want 422", got)
	}
}

// TestAgentScope: the owner sees its agent, another member gets 404 (no
// existence oracle) and an admin may only disable it (#59).
func TestAgentScope(t *testing.T) {
	f := newAgentsFixture(t, true)
	agent := f.createAgent(t, f.owner, "etl-agent")
	id := openapi_types.UUID(agent.Id)

	got, err := f.h.GetAgent(f.other, GetAgentRequestObject{AgentId: id})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got.(GetAgent404JSONResponse); !ok {
		t.Fatalf("other member get = %T, want 404", got)
	}

	if got, err := f.h.GetAgent(f.admin, GetAgentRequestObject{AgentId: id}); err != nil {
		t.Fatal(err)
	} else if _, ok := got.(GetAgent200JSONResponse); !ok {
		t.Fatalf("admin get = %T, want 200", got)
	}

	// An admin cannot rename someone else's agent.
	name := "renamed"
	if got, err := f.h.UpdateAgent(f.admin, UpdateAgentRequestObject{AgentId: id, Body: &UpdateAgentJSONRequestBody{Name: &name}}); err != nil {
		t.Fatal(err)
	} else if _, ok := got.(UpdateAgent403JSONResponse); !ok {
		t.Fatalf("admin rename = %T, want 403", got)
	}

	// But may disable it.
	disabled := UpdateAgentRequestStatusDisabled
	if got, err := f.h.UpdateAgent(f.admin, UpdateAgentRequestObject{AgentId: id, Body: &UpdateAgentJSONRequestBody{Status: &disabled}}); err != nil {
		t.Fatal(err)
	} else if _, ok := got.(UpdateAgent200JSONResponse); !ok {
		t.Fatalf("admin disable = %T, want 200", got)
	}

	// The owner can delete it; afterwards it is gone.
	if got, err := f.h.DeleteAgent(f.owner, DeleteAgentRequestObject{AgentId: id}); err != nil {
		t.Fatal(err)
	} else if _, ok := got.(DeleteAgent204Response); !ok {
		t.Fatalf("owner delete = %T, want 204", got)
	}
	if got, err := f.h.GetAgent(f.owner, GetAgentRequestObject{AgentId: id}); err != nil {
		t.Fatal(err)
	} else if _, ok := got.(GetAgent404JSONResponse); !ok {
		t.Fatalf("get after delete = %T, want 404", got)
	}
}

// TestRunQuery_AgentIsReadOnly: an agent execution forces a read-only target
// even when the workspace grants write, records agent_id and carries the agent
// identity into the workflow (#59).
func TestRunQuery_AgentIsReadOnly(t *testing.T) {
	fake := &fakeRunQueryQuerier{
		exec:    db.QueryExecution{ID: pgtype.UUID{Bytes: [16]byte{0xBB}, Valid: true}, Status: "running"},
		targets: []db.ListWorkspaceTargetsRow{targetRow(1, 1, "trino", "write", true)},
	}
	temporal := &fakeTemporalClient{}
	h := &Handlers{
		queries:        fake,
		cfg:            &config.Config{StatementTimeout: 30 * time.Minute},
		temporalClient: temporal,
	}
	agentCtx := context.WithValue(context.Background(), auth.ContextKeyUserID, testUserID)
	agentCtx = context.WithValue(agentCtx, auth.ContextKeyRole, "member")
	agentCtx = context.WithValue(agentCtx, auth.ContextKeyActorKind, auth.ActorAgent)
	agentCtx = context.WithValue(agentCtx, auth.ContextKeyAgentID, "22222222-2222-2222-2222-222222222222")

	if _, err := h.RunQuery(agentCtx, RunQueryRequestObject{
		Id:   PathId{0xAA},
		Body: &RunQueryJSONRequestBody{Sql: "SELECT 1"},
	}); err != nil {
		t.Fatal(err)
	}
	if len(fake.created) != 1 {
		t.Fatalf("executions created = %d, want 1", len(fake.created))
	}
	if !fake.created[0].AgentID.Valid {
		t.Error("query_executions.agent_id was not set")
	}
	input, ok := temporal.started[0].(workflows.QueryExecutionInput)
	if !ok {
		t.Fatalf("workflow input = %T", temporal.started[0])
	}
	if !input.Target.ReadOnly {
		t.Error("agent execution target is not read-only")
	}
	if input.Identity.AgentID == "" {
		t.Error("workflow identity has no agent id")
	}
}
