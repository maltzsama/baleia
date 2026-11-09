package api

import (
	"context"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/config"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/store/repo"
	"github.com/galileostudio/baleia/internal/testdb"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// TestServiceAccounts (#653): who may create and manage service accounts,
// their workspace membership and tokens, their identity-provider link, the
// global admin role they never get, and agents linked to them.
func TestServiceAccounts(t *testing.T) {
	for _, mode := range []struct {
		name      string
		withStore bool
	}{{"sqlc", false}, {"gorm", true}} {
		t.Run(mode.name, func(t *testing.T) {
			t.Parallel()
			runServiceAccounts(t, mode.withStore)
		})
	}
}

func runServiceAccounts(t *testing.T, withStore bool) {
	t.Helper()
	pool := testdb.New(t)
	q := repo.New(pool)
	ctx := context.Background()
	fields := handlerFields{queries: q, tx: storeTx{db: pool},
		catalogCache: NewTTLStore[[]workspaceCatalogEntry](time.Minute),
		settings:     SettingsFromConfig(&config.Config{AgentsEnabled: true})}
	if withStore {
		fields.store = pool
	}
	h := testAPI(fields)

	user := func(email, role string) db.User {
		pw := "x"
		u, err := q.CreateUser(ctx, db.CreateUserParams{Name: email, Email: email, PasswordHash: &pw, Role: role, AuthProvider: "local"})
		if err != nil {
			t.Fatal(err)
		}
		return u
	}
	admin, wsAdmin, editor, outsider := user("admin@t", "admin"), user("wsadmin@t", "member"), user("editor@t", "member"), user("outsider@t", "member")
	team, err := q.CreateTeam(ctx, db.CreateTeamParams{Name: "ops", CreatedBy: wsAdmin.ID})
	if err != nil {
		t.Fatal(err)
	}
	for u, r := range map[db.UUID]string{wsAdmin.ID: "owner", editor.ID: "member"} {
		if _, err := q.AddTeamMember(ctx, db.AddTeamMemberParams{TeamID: team.ID, UserID: u, Role: r}); err != nil {
			t.Fatal(err)
		}
	}
	ws, err := q.CreateWorkspace(ctx, db.CreateWorkspaceParams{Name: "Ops", Type: "shared", PolarisCatalogName: "ws_sa_ops", OwnerTeamID: team.ID})
	if err != nil {
		t.Fatal(err)
	}
	as := func(u db.User) context.Context { return ctxAs(idString(u.ID), u.Role) }
	wsID := openapi_types.UUID(ws.ID.Bytes)
	editorRole := WorkspaceRoleEditor

	// An editor may not create service accounts in the workspace; its admin may.
	if resp, _ := h.CreateServiceAccount(as(editor), CreateServiceAccountRequestObject{Body: &CreateServiceAccountJSONRequestBody{Name: "bot", WorkspaceId: &wsID}}); resp == nil {
		t.Fatal("nil")
	} else if _, ok := resp.(CreateServiceAccount403JSONResponse); !ok {
		t.Errorf("editor create = %T", resp)
	}
	if resp, _ := h.CreateServiceAccount(as(wsAdmin), CreateServiceAccountRequestObject{Body: &CreateServiceAccountJSONRequestBody{Name: "no workspace"}}); resp == nil {
		t.Fatal("nil")
	} else if _, ok := resp.(CreateServiceAccount403JSONResponse); !ok {
		t.Errorf("a workspace admin must name a workspace: %T", resp)
	}
	created, err := h.CreateServiceAccount(as(wsAdmin), CreateServiceAccountRequestObject{Body: &CreateServiceAccountJSONRequestBody{Name: "Reporting bot", WorkspaceId: &wsID, Role: &editorRole}})
	if err != nil {
		t.Fatal(err)
	}
	sa, ok := created.(CreateServiceAccount201JSONResponse)
	if !ok {
		t.Fatalf("create = %T %+v", created, created)
	}
	saID := db.UUID{Bytes: sa.Id, Valid: true}

	// It is an editor of the shared workspace without being in its team.
	if role, err := q.GetWorkspaceMemberRole(ctx, db.GetWorkspaceMemberRoleParams{ID: ws.ID, UserID: saID}); err != nil || role != "editor" {
		t.Errorf("service account role = %q, %v", role, err)
	}

	// Its managers see it; others do not.
	if resp, _ := h.GetServiceAccount(as(outsider), GetServiceAccountRequestObject{Id: sa.Id}); resp == nil {
		t.Fatal("nil")
	} else if _, ok := resp.(GetServiceAccount404JSONResponse); !ok {
		t.Errorf("outsider get = %T", resp)
	}
	for _, u := range []db.User{admin, wsAdmin} {
		if resp, _ := h.GetServiceAccount(as(u), GetServiceAccountRequestObject{Id: sa.Id}); resp == nil {
			t.Fatal("nil")
		} else if _, ok := resp.(GetServiceAccount200JSONResponse); !ok {
			t.Errorf("%s get = %T", u.Email, resp)
		}
	}
	list, err := h.ListServiceAccounts(as(wsAdmin), ListServiceAccountsRequestObject{})
	if err != nil {
		t.Fatal(err)
	}
	if rows := list.(ListServiceAccounts200JSONResponse); len(rows) != 1 || rows[0].Id != sa.Id {
		t.Errorf("workspace admin list = %+v", rows)
	}

	// A Baleia token, shown once.
	tok, err := h.CreateServiceAccountToken(as(wsAdmin), CreateServiceAccountTokenRequestObject{Id: sa.Id, Body: &CreateServiceAccountTokenJSONRequestBody{Name: "ci"}})
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := tok.(CreateServiceAccountToken201JSONResponse); !ok || got.Secret == "" {
		t.Fatalf("token = %+v", tok)
	}

	// An identity-provider client signs in as one service account only.
	ap, err := q.CreateAuthProvider(ctx, db.CreateAuthProviderParams{Name: "corp", Type: "oidc", ConfigEnc: "x", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	pid, client := openapi_types.UUID(ap.ID.Bytes), "reporting-bot"
	upd, err := h.UpdateServiceAccount(as(wsAdmin), UpdateServiceAccountRequestObject{Id: sa.Id, Body: &UpdateServiceAccountJSONRequestBody{Name: "Reporting bot", IdentityProviderId: &pid, ClientId: &client}})
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := upd.(UpdateServiceAccount200JSONResponse); !ok || got.ClientId == nil || *got.ClientId != client {
		t.Fatalf("link = %+v", upd)
	}
	if found, err := q.GetServiceAccountByClient(ctx, db.GetServiceAccountByClientParams{AuthProviderID: ap.ID, ExternalID: "client:" + client}); err != nil || found.ID != saID {
		t.Errorf("by client = %v, %v", found.ID, err)
	}
	dup, err := h.CreateServiceAccount(as(admin), CreateServiceAccountRequestObject{Body: &CreateServiceAccountJSONRequestBody{Name: "dup", IdentityProviderId: &pid, ClientId: &client}})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := dup.(CreateServiceAccount409JSONResponse); !ok {
		t.Errorf("duplicate client = %T", dup)
	}

	// Never a global admin.
	promote, err := h.UpdateUserRole(as(admin), UpdateUserRoleRequestObject{Id: sa.Id, Body: &UpdateUserRoleJSONRequestBody{Role: "admin"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := promote.(UpdateUserRole403JSONResponse); !ok {
		t.Errorf("promote = %T", promote)
	}

	// An agent linked to it; its managers manage the agent.
	agent, err := h.CreateAgent(as(wsAdmin), CreateAgentRequestObject{Body: &CreateAgentJSONRequestBody{Name: "reporter", ServiceAccountId: &sa.Id, WorkspaceIds: &[]openapi_types.UUID{wsID}}})
	if err != nil {
		t.Fatal(err)
	}
	a, ok := agent.(CreateAgent201JSONResponse)
	if !ok {
		t.Fatalf("agent = %T %+v", agent, agent)
	}
	row, err := q.GetAgentByID(ctx, db.UUID{Bytes: a.Id, Valid: true})
	if err != nil || row.OwnerID != saID {
		t.Errorf("agent owner = %v, %v", row.OwnerID, err)
	}
	if got, err := h.GetAgent(as(wsAdmin), GetAgentRequestObject{AgentId: a.Id}); err != nil {
		t.Fatal(err)
	} else if _, ok := got.(GetAgent200JSONResponse); !ok {
		t.Errorf("manager get agent = %T", got)
	}

	// Deleting it takes its memberships with it.
	del, err := h.DeleteServiceAccount(as(wsAdmin), DeleteServiceAccountRequestObject{Id: sa.Id})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := del.(DeleteServiceAccount204Response); !ok {
		t.Fatalf("delete = %T", del)
	}
	if role, _ := q.GetWorkspaceMemberRole(ctx, db.GetWorkspaceMemberRoleParams{ID: ws.ID, UserID: saID}); role != "" {
		t.Errorf("deleted account still has role %q", role)
	}
}
