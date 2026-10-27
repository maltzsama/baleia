package api

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/audit"
	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/config"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/testdb"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// memAudit keeps every event with the actor Record would store.
type memAudit struct {
	mu     sync.Mutex
	events []recorded
}

type recorded struct {
	audit.Event
	actorKind, actorID, method string
}

func (m *memAudit) Record(ctx context.Context, e audit.Event) error {
	kind, id, method := audit.ActorOf(ctx)
	m.mu.Lock()
	m.events = append(m.events, recorded{Event: e, actorKind: kind, actorID: id, method: method})
	m.mu.Unlock()
	return nil
}

// take returns the events recorded since the last take.
func (m *memAudit) take() []recorded {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := m.events
	m.events = nil
	return out
}

type auditFixture struct {
	pool                   *pgxpool.Pool
	h                      *API
	mem                    *memAudit
	admin, owner, stranger string
	ownerWS                openapi_types.UUID
}

func newAuditFixture(t *testing.T) *auditFixture {
	t.Helper()
	pool := testdb.New(t)
	ctx := context.Background()
	hash, err := auth.HashPassword("correct horse")
	if err != nil {
		t.Fatal(err)
	}
	f := &auditFixture{pool: pool, mem: &memAudit{}}
	user := func(email, role string) string {
		var id pgtype.UUID
		if err := pool.QueryRow(ctx, `INSERT INTO users (name, email, role, password_hash) VALUES ($1, $1, $2, $3) RETURNING id`, email, role, hash).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return idString(id)
	}
	f.admin, f.owner, f.stranger = user("admin@x.io", "admin"), user("owner@x.io", "member"), user("stranger@x.io", "member")
	var ws pgtype.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO workspaces (name, type, polaris_catalog_name, owner_user_id) VALUES ('w', 'personal', 'p', $1) RETURNING id`, f.owner).Scan(&ws); err != nil {
		t.Fatal(err)
	}
	f.ownerWS = openapi_types.UUID(ws.Bytes)
	f.h = testAPI(handlerFields{
		queries: db.New(pool), pool: pool, tx: poolTx{pool: pool}, schema: poolSchema{pool: pool}, keyring: testKeyring(t), connections: testRegistry(t),
		settings:     SettingsFromConfig(&config.Config{JWTSecret: strings.Repeat("s", 40)}),
		catalogCache: NewTTLStore[[]workspaceCatalogEntry](time.Minute),
		audit:        f.mem,
	})
	return f
}

// one asserts exactly one event with the action and outcome.
func one(t *testing.T, name string, got []recorded, action audit.Action, outcome audit.Outcome) recorded {
	t.Helper()
	if len(got) != 1 || got[0].Action != action || got[0].Outcome != outcome {
		t.Fatalf("%s: events = %+v, want one %s/%s", name, got, action, outcome)
	}
	return got[0]
}

// TestAuditMatrix: each operation, on success and on one failure path,
// yields exactly one event with the expected action and outcome.
func TestAuditMatrix(t *testing.T) {
	t.Parallel()
	f := newAuditFixture(t)
	h, mem := f.h, f.mem
	owner, stranger, admin := ctxAs(f.owner, "member"), ctxAs(f.stranger, "member"), ctxAs(f.admin, "admin")

	// Connections.
	resp, err := h.CreateCatalogConnection(owner, CreateCatalogConnectionRequestObject{Body: validCreateRequests()["polaris"]})
	if err != nil {
		t.Fatal(err)
	}
	created, ok := resp.(CreateCatalogConnection201JSONResponse)
	if !ok {
		t.Fatalf("create connection: %T", resp)
	}
	ev := one(t, "connection.create", mem.take(), audit.ConnectionCreate, audit.Allowed)
	if ev.ResourceID != created.Id.String() || ev.actorID != f.owner {
		t.Fatalf("connection.create = %+v", ev)
	}
	_, _ = h.CreateCatalogConnection(owner, CreateCatalogConnectionRequestObject{Body: &CreateCatalogConnectionRequest{Name: "bad", Type: "polaris"}})
	one(t, "connection.create invalid", mem.take(), audit.ConnectionCreate, audit.Error)

	name := "renamed"
	_, _ = h.UpdateCatalogConnection(owner, UpdateCatalogConnectionRequestObject{Id: created.Id, Body: &UpdateCatalogConnectionRequest{Name: &name}})
	ev = one(t, "connection.update", mem.take(), audit.ConnectionUpdate, audit.Allowed)
	if ev.Changes["name"].To != "renamed" {
		t.Fatalf("changes = %+v", ev.Changes)
	}
	_, _ = h.UpdateCatalogConnection(stranger, UpdateCatalogConnectionRequestObject{Id: created.Id, Body: &UpdateCatalogConnectionRequest{Name: &name}})
	one(t, "connection.update by a stranger", mem.take(), audit.ConnectionUpdate, audit.Denied)

	// Grants: a native catalog on the owner's connection.
	var cat pgtype.UUID
	if err := f.pool.QueryRow(context.Background(), `INSERT INTO catalogs (connection_id, name, origin) VALUES ($1, 'lake', 'native') RETURNING id`, created.Id).Scan(&cat); err != nil {
		t.Fatal(err)
	}
	catID := openapi_types.UUID(cat.Bytes)
	_, _ = h.GrantCatalogAccess(owner, GrantCatalogAccessRequestObject{Id: f.ownerWS, Body: &GrantCatalogAccessRequest{CatalogId: catID, Permission: "read"}})
	one(t, "grant.create", mem.take(), audit.GrantCreate, audit.Allowed)
	_, _ = h.GrantCatalogAccess(stranger, GrantCatalogAccessRequestObject{Id: f.ownerWS, Body: &GrantCatalogAccessRequest{CatalogId: catID, Permission: "read"}})
	one(t, "grant.create by a stranger", mem.take(), audit.GrantCreate, audit.Denied)
	_, _ = h.RevokeCatalogAccess(owner, RevokeCatalogAccessRequestObject{Id: f.ownerWS, CatalogId: catID})
	one(t, "grant.revoke", mem.take(), audit.GrantRevoke, audit.Allowed)
	_, _ = h.RevokeCatalogAccess(owner, RevokeCatalogAccessRequestObject{Id: f.ownerWS, CatalogId: catID})
	one(t, "grant.revoke twice", mem.take(), audit.GrantRevoke, audit.Error)

	// Workspace.
	wsName := "renamed ws"
	_, _ = h.UpdateWorkspace(owner, UpdateWorkspaceRequestObject{Id: f.ownerWS, Body: &UpdateWorkspaceRequest{Name: &wsName}})
	ev = one(t, "workspace.update", mem.take(), audit.WorkspaceUpdate, audit.Allowed)
	if ev.Changes["name"].To != wsName {
		t.Fatalf("changes = %+v", ev.Changes)
	}
	_, _ = h.UpdateWorkspace(stranger, UpdateWorkspaceRequestObject{Id: f.ownerWS, Body: &UpdateWorkspaceRequest{Name: &wsName}})
	one(t, "workspace.update by a stranger", mem.take(), audit.WorkspaceUpdate, audit.Denied)

	// Users.
	_, _ = h.InviteUser(admin, InviteUserRequestObject{Body: &InviteUserRequest{Email: "new@x.io", Role: "member"}})
	one(t, "user.invite", mem.take(), audit.UserInvite, audit.Allowed)
	_, _ = h.InviteUser(owner, InviteUserRequestObject{Body: &InviteUserRequest{Email: "other@x.io", Role: "member"}})
	one(t, "user.invite by a member", mem.take(), audit.UserInvite, audit.Denied)
	strangerID := openapi_types.UUID(mustUUID(t, f.stranger).Bytes)
	_, _ = h.UpdateUserRole(admin, UpdateUserRoleRequestObject{Id: strangerID, Body: &UpdateUserRoleRequest{Role: "admin"}})
	ev = one(t, "user.role_change", mem.take(), audit.UserRoleChange, audit.Allowed)
	if ev.Changes["role"].From != "member" || ev.Changes["role"].To != "admin" {
		t.Fatalf("changes = %+v", ev.Changes)
	}

	// Identity providers (#259).
	idp := func() *CreateIdentityProviderRequest {
		return &CreateIdentityProviderRequest{
			Name: "okta", Type: IdentityProviderTypeOidc,
			Oidc: &OIDCConfig{IssuerUrl: "https://idp.example.test", ClientId: "client", ClientSecret: "secret"},
		}
	}
	_, _ = h.CreateIdentityProvider(admin, CreateIdentityProviderRequestObject{Body: idp()})
	one(t, "identity_provider.create", mem.take(), audit.IdentityProviderCreate, audit.Allowed)
	_, _ = h.CreateIdentityProvider(owner, CreateIdentityProviderRequestObject{Body: idp()})
	one(t, "identity_provider.create by a member", mem.take(), audit.IdentityProviderCreate, audit.Denied)

	// API keys.
	kr, _ := h.CreateApiKey(owner, CreateApiKeyRequestObject{Body: &CreateApiKeyRequest{Name: "ci"}})
	one(t, "api_key.create", mem.take(), audit.APIKeyCreate, audit.Allowed)
	key := kr.(CreateApiKey201JSONResponse)
	_, _ = h.DeleteApiKey(owner, DeleteApiKeyRequestObject{Id: key.Id})
	one(t, "api_key.revoke", mem.take(), audit.APIKeyRevoke, audit.Allowed)

	// Auth.
	_, _ = h.Login(context.Background(), LoginRequestObject{Body: &LoginRequest{Email: emailPtr("owner@x.io"), Password: "correct horse"}})
	ev = one(t, "auth.login", mem.take(), audit.AuthLogin, audit.Allowed)
	if ev.actorKind != "user" || ev.actorID != f.owner || ev.method != audit.AuthPassword {
		t.Fatalf("login actor = %+v", ev)
	}

	// Connection delete last (the catalog cascades).
	_, _ = h.DeleteCatalogConnection(stranger, DeleteCatalogConnectionRequestObject{Id: created.Id})
	one(t, "connection.delete by a stranger", mem.take(), audit.ConnectionDelete, audit.Denied)
	_, _ = h.DeleteCatalogConnection(owner, DeleteCatalogConnectionRequestObject{Id: created.Id})
	one(t, "connection.delete", mem.take(), audit.ConnectionDelete, audit.Allowed)
}

// TestAuditDenialsOnce: a refused request is one denied event, recorded by
// the checker hook under the handler's action (not zero, not two).
func TestAuditDenialsOnce(t *testing.T) {
	t.Parallel()
	f := newAuditFixture(t)
	resp, _ := f.h.CreateCatalogConnection(ctxAs(f.owner, "member"), CreateCatalogConnectionRequestObject{Body: validCreateRequests()["polaris"]})
	id := resp.(CreateCatalogConnection201JSONResponse).Id
	f.mem.take()
	_, _ = f.h.TestCatalogConnection(ctxAs(f.stranger, "member"), TestCatalogConnectionRequestObject{Id: id})
	ev := one(t, "connection.test by a stranger", f.mem.take(), audit.ConnectionTest, audit.Denied)
	if ev.Reason != audit.ReasonNotFound || ev.ResourceType != "connection" || ev.ResourceID != id.String() {
		t.Fatalf("denial = %+v", ev)
	}
	// A refused read is not audited: reads have no action.
	_, _ = f.h.GetCatalogConnection(ctxAs(f.stranger, "member"), GetCatalogConnectionRequestObject{Id: id})
	if got := f.mem.take(); len(got) != 0 {
		t.Fatalf("a refused read was audited: %+v", got)
	}
}

// TestAuditNoSecrets: updating credentials records the secret keys as
// "***"; no recorded event holds a secret value.
func TestAuditNoSecrets(t *testing.T) {
	t.Parallel()
	f := newAuditFixture(t)
	owner := ctxAs(f.owner, "member")
	resp, _ := f.h.CreateCatalogConnection(owner, CreateCatalogConnectionRequestObject{Body: validCreateRequests()["polaris"]})
	id := resp.(CreateCatalogConnection201JSONResponse).Id
	_, _ = f.h.UpdateCatalogConnection(owner, UpdateCatalogConnectionRequestObject{Id: id, Body: &UpdateCatalogConnectionRequest{Credentials: anyMap("client_secret", "n3w-s3cr3t")}})
	events := f.mem.take()
	ev := events[len(events)-1]
	if ev.Action != audit.ConnectionUpdate || ev.Changes["credentials.client_secret"].To != "***" {
		t.Fatalf("update = %+v", ev)
	}
	raw, _ := json.Marshal(events)
	for _, secret := range []string{"p-s3cr3t", "n3w-s3cr3t"} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("an event holds %q: %s", secret, raw)
		}
	}
}

// TestAuditLoginFailureHashesEmail: a failed login is recorded with the
// e-mail's fingerprint, never the e-mail itself.
func TestAuditLoginFailureHashesEmail(t *testing.T) {
	t.Parallel()
	f := newAuditFixture(t)
	_, _ = f.h.Login(context.Background(), LoginRequestObject{Body: &LoginRequest{Email: emailPtr("owner@x.io"), Password: "wrong"}})
	_, _ = f.h.Login(context.Background(), LoginRequestObject{Body: &LoginRequest{Email: emailPtr("nobody@x.io"), Password: "wrong"}})
	events := f.mem.take()
	if len(events) != 2 {
		t.Fatalf("events = %+v", events)
	}
	for i, email := range []string{"owner@x.io", "nobody@x.io"} {
		ev := events[i]
		if ev.Action != audit.AuthLogin || ev.Outcome != audit.Denied || ev.actorKind != "anonymous" || ev.actorID != audit.EmailFingerprint(email) {
			t.Fatalf("event %d = %+v", i, ev)
		}
	}
	raw, _ := json.Marshal(events)
	if strings.Contains(string(raw), "@x.io") || strings.Contains(string(raw), "wrong") {
		t.Fatalf("a raw e-mail or password was recorded: %s", raw)
	}
}

func mustUUID(t *testing.T, s string) pgtype.UUID {
	t.Helper()
	var id pgtype.UUID
	if err := id.Scan(s); err != nil {
		t.Fatal(err)
	}
	return id
}
