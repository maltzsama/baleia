package api

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/config"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/dialect"
	"github.com/galileostudio/baleia/internal/temporal/workflows"
	"github.com/galileostudio/baleia/internal/testdb"
	wsaccess "github.com/galileostudio/baleia/internal/workspaces/access"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

type agentsFixture struct {
	pool    *pgxpool.Pool
	h       *API
	owner   context.Context
	other   context.Context
	admin   context.Context
	ownerID pgtype.UUID
	otherID pgtype.UUID
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
	f.otherID = otherID
	adminID := user("admin@agents.test", "admin")
	f.owner = ctxAs(idString(f.ownerID), "member")
	f.other = ctxAs(idString(otherID), "member")
	f.admin = ctxAs(idString(adminID), "admin")

	if err := pool.QueryRow(ctx,
		`INSERT INTO workspaces (name, type, polaris_catalog_name, owner_user_id)
		 VALUES ('agents-ws', 'personal', 'agents_polaris', $1) RETURNING id`, f.ownerID).Scan(&f.wsID); err != nil {
		t.Fatal(err)
	}

	f.h = testAPI(handlerFields{
		queries:  db.New(pool),
		pool:     pool,
		tx:       poolTx{pool: pool},
		settings: SettingsFromConfig(&config.Config{AgentsEnabled: enabled}),
	})
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

// TestUpdateAgent_InvalidWorkspaceLeavesNoWrite: a refused workspace change
// must not persist the name/status sent in the same PATCH (#59 review).
func TestUpdateAgent_InvalidWorkspaceLeavesNoWrite(t *testing.T) {
	f := newAgentsFixture(t, true)
	agent := f.createAgent(t, f.owner, "etl-agent")
	id := openapi_types.UUID(agent.Id)

	var foreign pgtype.UUID
	if err := f.pool.QueryRow(context.Background(),
		`INSERT INTO workspaces (name, type, polaris_catalog_name, owner_user_id)
		 VALUES ('foreign-ws', 'personal', 'foreign_polaris', $1) RETURNING id`, f.otherID).Scan(&foreign); err != nil {
		t.Fatal(err)
	}

	name := "renamed"
	got, err := f.h.UpdateAgent(f.owner, UpdateAgentRequestObject{
		AgentId: id,
		Body: &UpdateAgentJSONRequestBody{
			Name:         &name,
			WorkspaceIds: &[]openapi_types.UUID{openapi_types.UUID(foreign.Bytes)},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got.(UpdateAgent404JSONResponse); !ok {
		t.Fatalf("PATCH with a foreign workspace = %T, want 404", got)
	}
	reloaded, err := f.h.GetAgent(f.owner, GetAgentRequestObject{AgentId: id})
	if err != nil {
		t.Fatal(err)
	}
	if name := Agent(reloaded.(GetAgent200JSONResponse)).Name; name != "etl-agent" {
		t.Fatalf("name persisted after a refused workspace change: %q", name)
	}
}

// TestUpdateAgent_UnknownStatusIs422: a status outside the enum is refused
// before any write, not passed to the table's CHECK as a 500.
func TestUpdateAgent_UnknownStatusIs422(t *testing.T) {
	f := newAgentsFixture(t, true)
	agent := f.createAgent(t, f.owner, "etl-agent")
	status := UpdateAgentRequestStatus("paused")
	got, err := f.h.UpdateAgent(f.owner, UpdateAgentRequestObject{
		AgentId: openapi_types.UUID(agent.Id),
		Body:    &UpdateAgentJSONRequestBody{Status: &status},
	})
	if err != nil {
		t.Fatalf("UpdateAgent = %v, want a 422", err)
	}
	if _, ok := got.(UpdateAgent422JSONResponse); !ok {
		t.Fatalf("unknown status = %T, want 422", got)
	}
}

// TestRequireWorkspaceAccess_AgentBinding: the execution path applies the
// agent's workspace binding, not only its owner's membership (#59 review).
func TestRequireWorkspaceAccess_AgentBinding(t *testing.T) {
	fake := &fakeRunQueryQuerier{}
	ctx := context.WithValue(context.Background(), auth.ContextKeyUserID, testUserID)
	ctx = context.WithValue(ctx, auth.ContextKeyRole, "member")
	ctx = context.WithValue(ctx, auth.ContextKeyActorKind, auth.ActorAgent)
	ctx = context.WithValue(ctx, auth.ContextKeyAgentID, "22222222-2222-2222-2222-222222222222")
	ws := openapi_types.UUID{0xAA}

	if err := requireWorkspaceAccess(ctx, wsaccess.New(fake), ws); !errors.Is(err, ErrWorkspaceNotFound) {
		t.Fatalf("unbound agent = %v, want ErrWorkspaceNotFound", err)
	}
	fake.agentBound = true
	if err := requireWorkspaceAccess(ctx, wsaccess.New(fake), ws); err != nil {
		t.Fatalf("bound agent = %v", err)
	}
}

// TestRunQuery_AgentIsReadOnly: an agent execution forces a read-only target
// even when the workspace grants write, records agent_id and carries the agent
// identity into the workflow (#59).
func TestRunQuery_AgentIsReadOnly(t *testing.T) {
	fake := &fakeRunQueryQuerier{
		exec:       db.QueryExecution{ID: pgtype.UUID{Bytes: [16]byte{0xBB}, Valid: true}, Status: "running"},
		targets:    []db.ListWorkspaceTargetsRow{targetRow(1, 1, "trino", "write", true)},
		agentBound: true,
	}
	temporal := &fakeTemporalClient{}
	h := testAPI(handlerFields{
		queries: fake, tx: directTx{q: fake},
		settings:       SettingsFromConfig(&config.Config{StatementTimeout: 30 * time.Minute}),
		temporalClient: temporal,
		kinds:          testKinds(t, nil),
		dialects:       dialect.NewRegistry(),
	})
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

func TestAgentPolicy_DefaultsAndCeilings(t *testing.T) {
	f := newAgentsFixture(t, true)
	agent := f.createAgent(t, f.owner, "etl-agent")

	got, err := f.h.GetAgentPolicy(f.owner, GetAgentPolicyRequestObject{AgentId: openapi_types.UUID(agent.Id)})
	if err != nil {
		t.Fatal(err)
	}
	resp, ok := got.(GetAgentPolicy200JSONResponse)
	if !ok {
		t.Fatalf("get policy = %T", got)
	}
	if resp.Policy.MaxRowsReturned == nil || *resp.Policy.MaxRowsReturned != 500 {
		t.Fatalf("effective rows = %v, want 500", resp.Policy.MaxRowsReturned)
	}
	if resp.Ceilings.MaxRowsReturned == nil || *resp.Ceilings.MaxRowsReturned != 5000 {
		t.Fatalf("ceiling rows = %v, want 5000", resp.Ceilings.MaxRowsReturned)
	}
	if resp.Stored.Tools != nil {
		t.Errorf("stored tools = %v, want nil (defaults)", resp.Stored.Tools)
	}
}

func TestAgentPolicy_PutAndClamp(t *testing.T) {
	f := newAgentsFixture(t, true)
	agent := f.createAgent(t, f.owner, "etl-agent")

	version := AgentPolicyVersion(1)
	tools := []string{"list_catalogs", "run_query"}
	rows := 10
	got, err := f.h.PutAgentPolicy(f.owner, PutAgentPolicyRequestObject{
		AgentId: openapi_types.UUID(agent.Id),
		Body:    &PutAgentPolicyJSONRequestBody{Version: version, Tools: &tools, MaxRowsReturned: &rows},
	})
	if err != nil {
		t.Fatal(err)
	}
	resp, ok := got.(PutAgentPolicy200JSONResponse)
	if !ok {
		t.Fatalf("put policy = %T", got)
	}
	if resp.Policy.MaxRowsReturned == nil || *resp.Policy.MaxRowsReturned != 10 {
		t.Fatalf("effective rows = %v, want 10", resp.Policy.MaxRowsReturned)
	}
	if resp.Stored.MaxRowsReturned == nil || *resp.Stored.MaxRowsReturned != 10 {
		t.Fatalf("stored rows = %v, want 10", resp.Stored.MaxRowsReturned)
	}
	if len(*resp.Stored.Tools) != 2 {
		t.Fatalf("stored tools = %v, want 2", *resp.Stored.Tools)
	}
}

func TestAgentPolicy_Validation(t *testing.T) {
	f := newAgentsFixture(t, true)
	agent := f.createAgent(t, f.owner, "etl-agent")
	version := AgentPolicyVersion(1)
	id := openapi_types.UUID(agent.Id)

	rows := 10000
	got, err := f.h.PutAgentPolicy(f.owner, PutAgentPolicyRequestObject{
		AgentId: id,
		Body:    &PutAgentPolicyJSONRequestBody{Version: version, MaxRowsReturned: &rows},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got.(PutAgentPolicy422JSONResponse); !ok {
		t.Fatalf("above the ceiling = %T, want 422", got)
	}

	tools := []string{"not_a_tool"}
	got, err = f.h.PutAgentPolicy(f.owner, PutAgentPolicyRequestObject{
		AgentId: id,
		Body:    &PutAgentPolicyJSONRequestBody{Version: version, Tools: &tools},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got.(PutAgentPolicy422JSONResponse); !ok {
		t.Fatalf("unknown tool = %T, want 422", got)
	}
}

func TestAgentPolicy_Scope(t *testing.T) {
	f := newAgentsFixture(t, true)
	agent := f.createAgent(t, f.owner, "etl-agent")
	id := openapi_types.UUID(agent.Id)

	if got, err := f.h.GetAgentPolicy(f.other, GetAgentPolicyRequestObject{AgentId: id}); err != nil {
		t.Fatal(err)
	} else if _, ok := got.(GetAgentPolicy404JSONResponse); !ok {
		t.Fatalf("other member get = %T, want 404", got)
	}
	if got, err := f.h.GetAgentPolicy(f.admin, GetAgentPolicyRequestObject{AgentId: id}); err != nil {
		t.Fatal(err)
	} else if _, ok := got.(GetAgentPolicy200JSONResponse); !ok {
		t.Fatalf("admin get = %T, want 200", got)
	}
}
