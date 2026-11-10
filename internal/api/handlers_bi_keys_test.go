package api

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/store/model"
	"github.com/galileostudio/baleia/internal/store/repo"
	"github.com/galileostudio/baleia/internal/testdb"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// TestBIKeys covers the workspace-bound BI key endpoints (#708): a member
// creates and revokes their own key, a workspace admin creates for a service
// account and sees every key, and an outsider sees nothing.
func TestBIKeys(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	q := repo.New(pool)
	ctx := context.Background()
	h := testAPI(handlerFields{queries: q, store: pool, tx: storeTx{db: pool}, catalogCache: NewTTLStore[[]workspaceCatalogEntry](time.Minute)})

	user := func(email string) db.User {
		pw := "x"
		u, err := q.CreateUser(ctx, db.CreateUserParams{Name: email, Email: email, PasswordHash: &pw, Role: "member", AuthProvider: "local"})
		if err != nil {
			t.Fatal(err)
		}
		return u
	}
	owner, member, outsider := user("bi-owner@t"), user("bi-member@t"), user("bi-outsider@t")
	sa := user("bi-sa@service-accounts.baleia.invalid")
	sa.Kind = "service_account"
	if err := pool.Model(&model.User{}).Where("id = ?", string(model.UUID(idString(sa.ID)))).Update("kind", "service_account").Error; err != nil {
		t.Fatal(err)
	}

	ws, err := q.CreateWorkspace(ctx, db.CreateWorkspaceParams{Name: "BI", Type: "personal", PolarisCatalogName: "ws_bi_test", OwnerUserID: owner.ID})
	if err != nil {
		t.Fatal(err)
	}
	for u, role := range map[db.UUID]string{member.ID: "editor", sa.ID: "viewer"} {
		if _, err := q.UpsertWorkspaceMember(ctx, db.UpsertWorkspaceMemberParams{WorkspaceID: ws.ID, UserID: u, Role: role, GrantedBy: owner.ID}); err != nil {
			t.Fatal(err)
		}
	}

	now := time.Now().UTC()
	conn := model.CatalogConnection{
		ID: model.UUID(repo.NewUUID()), Name: model.Text("bi-conn-" + repo.NewUUID()[:8]), Type: "trino",
		URL: "http://trino.test", CreatedAt: now, UpdatedAt: now,
	}
	if err := pool.Create(&conn).Error; err != nil {
		t.Fatal(err)
	}
	engine := model.Engine{
		ID: model.UUID(repo.NewUUID()), Name: model.Text("bi-trino-" + repo.NewUUID()[:8]), Kind: "trino", Class: "federated",
		ConnectionID: conn.ID, CreatedAt: now, UpdatedAt: now,
	}
	if err := pool.Create(&engine).Error; err != nil {
		t.Fatal(err)
	}
	catalog := model.Catalog{
		ID: model.UUID(repo.NewUUID()), ConnectionID: conn.ID, Name: model.Text("bi_lake"), Origin: "native",
		Enabled: true, CreatedAt: now, UpdatedAt: now,
	}
	if err := pool.Create(&catalog).Error; err != nil {
		t.Fatal(err)
	}
	if err := pool.Create(&model.CatalogBinding{
		ID: model.UUID(repo.NewUUID()), CatalogID: catalog.ID, EngineID: engine.ID,
		EngineCatalogName: "tpch_test", Mode: "native", Enabled: true, LifecycleState: "active",
		CreatedAt: now, UpdatedAt: now, StateChangedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := pool.Create(&model.WorkspaceCatalogGrant{
		WorkspaceID: model.UUID(repo.UUIDString(ws.ID)), CatalogID: catalog.ID, GrantedBy: model.UUID(repo.UUIDString(owner.ID)), GrantedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}

	as := func(u db.User) context.Context { return ctxAs(idString(u.ID), u.Role) }
	wsPath := PathId(ws.ID.Bytes)
	engineID := openapi_types.UUID(repo.PGUUID(string(engine.ID)).Bytes)
	create := func(caller db.User, body *CreateBiKeyJSONRequestBody) CreateBiKeyResponseObject {
		t.Helper()
		resp, err := h.CreateBiKey(as(caller), CreateBiKeyRequestObject{Id: wsPath, Body: body})
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}

	// The owner (workspace admin) creates a key for themselves.
	ownerKey, ok := create(owner, &CreateBiKeyJSONRequestBody{Name: "Tableau", EngineId: engineID}).(CreateBiKey201JSONResponse)
	if !ok {
		t.Fatalf("owner create = %T", create(owner, &CreateBiKeyJSONRequestBody{Name: "Tableau", EngineId: engineID}))
	}
	if ownerKey.Name != "Tableau" || ownerKey.WorkspaceId != openapi_types.UUID(ws.ID.Bytes) || ownerKey.EngineId != engineID {
		t.Fatalf("created = %+v", ownerKey)
	}
	if !strings.HasPrefix(ownerKey.Secret, "bal_") || !strings.HasPrefix(ownerKey.KeyPrefix, "bal_") {
		t.Fatalf("secret/prefix = %q/%q", ownerKey.Secret, ownerKey.KeyPrefix)
	}
	cred, err := h.BIKeyService().Authenticate(ctx, owner.Email, ownerKey.Secret)
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	if cred.Key.UserID.Bytes != owner.ID.Bytes || cred.Key.WorkspaceID.Bytes != ws.ID.Bytes || cred.Key.EngineID.Bytes != repo.PGUUID(string(engine.ID)).Bytes {
		t.Fatalf("credential = %+v", cred)
	}

	// A member creates a key for themselves but not for a service account.
	memberKey, ok := create(member, &CreateBiKeyJSONRequestBody{Name: "Power BI", EngineId: engineID}).(CreateBiKey201JSONResponse)
	if !ok {
		t.Fatal("a member may create their own key")
	}
	saID := openapi_types.UUID(sa.ID.Bytes)
	if got := create(member, &CreateBiKeyJSONRequestBody{Name: "sa", EngineId: engineID, OwnerId: &saID}); got == nil {
		t.Fatal("expected a refusal")
	} else if _, ok := got.(CreateBiKey403JSONResponse); !ok {
		t.Fatalf("member for a service account = %T", got)
	}
	// The owner, a workspace admin, may create for the service account.
	saKey, ok := create(owner, &CreateBiKeyJSONRequestBody{Name: "sa", EngineId: engineID, OwnerId: &saID}).(CreateBiKey201JSONResponse)
	if !ok || saKey.OwnerId != saID {
		t.Fatalf("admin for a service account = %+v", saKey)
	}

	// List: a member sees only their own; an admin sees every key.
	list := func(caller db.User) []BiKey {
		t.Helper()
		resp, err := h.ListBiKeys(as(caller), ListBiKeysRequestObject{Id: wsPath})
		if err != nil {
			t.Fatal(err)
		}
		got, ok := resp.(ListBiKeys200JSONResponse)
		if !ok {
			t.Fatalf("list = %T", resp)
		}
		return got
	}
	if got := list(member); len(got) != 1 || got[0].Id != memberKey.Id {
		t.Fatalf("member list = %+v", got)
	}
	if got := list(owner); len(got) != 3 {
		t.Fatalf("admin list = %+v", got)
	}

	// An outsider cannot reach the workspace.
	if got, err := h.ListBiKeys(as(outsider), ListBiKeysRequestObject{Id: wsPath}); err != nil {
		t.Fatal(err)
	} else if _, ok := got.(ListBiKeys404JSONResponse); !ok {
		t.Fatalf("outsider list = %T", got)
	}

	// Revocation: a member cannot revoke another key; the admin can.
	del := func(caller db.User, id openapi_types.UUID) DeleteBiKeyResponseObject {
		t.Helper()
		resp, err := h.DeleteBiKey(as(caller), DeleteBiKeyRequestObject{Id: wsPath, KeyId: id})
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	if _, ok := del(member, ownerKey.Id).(DeleteBiKey403JSONResponse); !ok {
		t.Fatal("a member revoked another key")
	}
	if _, ok := del(member, memberKey.Id).(DeleteBiKey204Response); !ok {
		t.Fatal("a member could not revoke their own key")
	}
	if _, ok := del(owner, saKey.Id).(DeleteBiKey204Response); !ok {
		t.Fatal("an admin could not revoke the service-account key")
	}
	if _, ok := del(owner, saKey.Id).(DeleteBiKey404JSONResponse); !ok {
		t.Fatal("revoking twice must be 404")
	}
}

// TestBIKeyRequiresTrinoTarget: a key cannot be issued for a non-Trino engine
// or an engine with no binding in the workspace scope (#708).
func TestBIKeyRequiresTrinoTarget(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	q := repo.New(pool)
	ctx := context.Background()
	h := testAPI(handlerFields{queries: q, store: pool, tx: storeTx{db: pool}, catalogCache: NewTTLStore[[]workspaceCatalogEntry](time.Minute)})

	pw := "x"
	owner, err := q.CreateUser(ctx, db.CreateUserParams{Name: "owner", Email: "bi-target@t", PasswordHash: &pw, Role: "member", AuthProvider: "local"})
	if err != nil {
		t.Fatal(err)
	}
	ws, err := q.CreateWorkspace(ctx, db.CreateWorkspaceParams{Name: "BI", Type: "personal", PolarisCatalogName: "ws_bi_target", OwnerUserID: owner.ID})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	conn := model.CatalogConnection{
		ID: model.UUID(repo.NewUUID()), Name: model.Text("bi-target-conn-" + repo.NewUUID()[:8]), Type: "postgresql",
		URL: "postgres://db.test", CreatedAt: now, UpdatedAt: now,
	}
	if err := pool.Create(&conn).Error; err != nil {
		t.Fatal(err)
	}
	engine := model.Engine{
		ID: model.UUID(repo.NewUUID()), Name: model.Text("pg-" + repo.NewUUID()[:8]), Kind: "postgresql", Class: "direct",
		ConnectionID: conn.ID, CreatedAt: now, UpdatedAt: now,
	}
	if err := pool.Create(&engine).Error; err != nil {
		t.Fatal(err)
	}
	body := &CreateBiKeyJSONRequestBody{Name: "bad", EngineId: openapi_types.UUID(repo.PGUUID(string(engine.ID)).Bytes)}
	resp, err := h.CreateBiKey(ctxAs(idString(owner.ID), owner.Role), CreateBiKeyRequestObject{Id: PathId(ws.ID.Bytes), Body: body})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := resp.(CreateBiKey422JSONResponse); !ok {
		t.Fatalf("non-trino target = %T, want 422", resp)
	}
}
