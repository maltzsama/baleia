package authz

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func uid(b byte) pgtype.UUID { return pgtype.UUID{Bytes: [16]byte{b}, Valid: true} }

type store struct {
	conns   map[[16]byte]db.CatalogConnection
	cats    map[[16]byte]db.Catalog
	granted map[[2][16]byte]bool // {catalog, user} -> a workspace of the user holds a grant
	wss     map[[16]byte]db.Workspace
	members map[[2][16]byte]bool // {workspace, user}
	owners  map[[2][16]byte]bool // {workspace, user}
	teams   map[[16]byte]db.Team
	roles   map[[2][16]byte]string // {team, user} -> team role
	engines map[[16]byte]db.Engine
	hosted  map[[2][16]byte]bool // {engine, user} -> the engine hosts a catalog granted to the user
	// {workspace, space, user} -> effective space role
	spaceRoles map[[3][16]byte]string
	// {agent, workspace} -> the agent is bound to the workspace (#59)
	agentBound map[[2][16]byte]bool
	// {resource, agent} -> the catalog/engine is reachable through a bound
	// workspace (#59)
	catalogGrantedToAgent map[[2][16]byte]bool
	engineHostsForAgent   map[[2][16]byte]bool
}

func (s store) AgentWorkspaceBound(_ context.Context, a db.AgentWorkspaceBoundParams) (bool, error) {
	return s.agentBound[[2][16]byte{a.AgentID.Bytes, a.WorkspaceID.Bytes}], nil
}

func (s store) CatalogGrantedToAgent(_ context.Context, a db.CatalogGrantedToAgentParams) (bool, error) {
	return s.catalogGrantedToAgent[[2][16]byte{a.CatalogID.Bytes, a.AgentID.Bytes}], nil
}

func (s store) EngineHostsCatalogGrantedToAgent(_ context.Context, a db.EngineHostsCatalogGrantedToAgentParams) (bool, error) {
	return s.engineHostsForAgent[[2][16]byte{a.EngineID.Bytes, a.AgentID.Bytes}], nil
}

func (s store) GetEffectiveSpaceRole(_ context.Context, a db.GetEffectiveSpaceRoleParams) (string, error) {
	return s.spaceRoles[[3][16]byte{a.WorkspaceID.Bytes, a.SpaceID.Bytes, a.UserID.Bytes}], nil
}

func (s store) GetEngineByID(_ context.Context, id pgtype.UUID) (db.Engine, error) {
	if e, ok := s.engines[id.Bytes]; ok {
		return e, nil
	}
	return db.Engine{}, pgx.ErrNoRows
}

func (s store) EngineHostsCatalogGrantedToUser(_ context.Context, a db.EngineHostsCatalogGrantedToUserParams) (bool, error) {
	return s.hosted[[2][16]byte{a.EngineID.Bytes, a.UserID.Bytes}], nil
}

func (s store) CatalogGrantedToUser(_ context.Context, a db.CatalogGrantedToUserParams) (bool, error) {
	return s.granted[[2][16]byte{a.CatalogID.Bytes, a.UserID.Bytes}], nil
}

func (s store) GetWorkspaceByID(_ context.Context, id pgtype.UUID) (db.Workspace, error) {
	if w, ok := s.wss[id.Bytes]; ok {
		return w, nil
	}
	return db.Workspace{}, pgx.ErrNoRows
}

func (s store) CheckWorkspaceAccess(_ context.Context, a db.CheckWorkspaceAccessParams) (bool, error) {
	return s.members[[2][16]byte{a.ID.Bytes, a.UserID.Bytes}], nil
}

func (s store) IsWorkspaceOwner(_ context.Context, a db.IsWorkspaceOwnerParams) (bool, error) {
	return s.owners[[2][16]byte{a.ID.Bytes, a.UserID.Bytes}], nil
}

func (s store) GetTeamByID(_ context.Context, id pgtype.UUID) (db.Team, error) {
	if t, ok := s.teams[id.Bytes]; ok {
		return t, nil
	}
	return db.Team{}, pgx.ErrNoRows
}

func (s store) GetTeamMemberRole(_ context.Context, a db.GetTeamMemberRoleParams) (string, error) {
	if r, ok := s.roles[[2][16]byte{a.TeamID.Bytes, a.UserID.Bytes}]; ok {
		return r, nil
	}
	return "", pgx.ErrNoRows
}

func (s store) GetCatalogConnectionByID(_ context.Context, id pgtype.UUID) (db.CatalogConnection, error) {
	if c, ok := s.conns[id.Bytes]; ok {
		return c, nil
	}
	return db.CatalogConnection{}, pgx.ErrNoRows
}

func (s store) GetCatalogByID(_ context.Context, id pgtype.UUID) (db.Catalog, error) {
	if c, ok := s.cats[id.Bytes]; ok {
		return c, nil
	}
	return db.Catalog{}, pgx.ErrNoRows
}

var (
	owner  = Principal{UserID: uid(1), Role: "member"}
	other  = Principal{UserID: uid(2), Role: "member"}
	admin  = Principal{UserID: uid(3), Role: RoleAdmin}
	connA  = uid(10) // owned by owner
	connB  = uid(11) // owned by other
	connNo = uid(12) // created_by missing (legacy row)
	catA   = uid(20) // under connA
	wsX    = uid(30) // workspace: reader is a member, wsOwn owns it
	teamT  = uid(40)
	reader = Principal{UserID: uid(4), Role: "member"} // member of wsX, which holds a grant on catA
	engA   = uid(50)                                   // runs on connA and hosts catA
	wsOwn  = Principal{UserID: uid(5), Role: "member"} // owner of wsX
)

func fixture() *Checker {
	return New(store{
		conns: map[[16]byte]db.CatalogConnection{
			connA.Bytes:  {ID: connA, CreatedBy: owner.UserID},
			connB.Bytes:  {ID: connB, CreatedBy: other.UserID},
			connNo.Bytes: {ID: connNo},
		},
		cats:    map[[16]byte]db.Catalog{catA.Bytes: {ID: catA, ConnectionID: connA}},
		granted: map[[2][16]byte]bool{{catA.Bytes, reader.UserID.Bytes}: true},
		wss:     map[[16]byte]db.Workspace{wsX.Bytes: {ID: wsX}},
		members: map[[2][16]byte]bool{{wsX.Bytes, reader.UserID.Bytes}: true, {wsX.Bytes, wsOwn.UserID.Bytes}: true},
		owners:  map[[2][16]byte]bool{{wsX.Bytes, wsOwn.UserID.Bytes}: true},
		teams:   map[[16]byte]db.Team{teamT.Bytes: {ID: teamT}},
		roles:   map[[2][16]byte]string{{teamT.Bytes, reader.UserID.Bytes}: "member", {teamT.Bytes, wsOwn.UserID.Bytes}: "owner"},
		engines: map[[16]byte]db.Engine{engA.Bytes: {ID: engA, ConnectionID: connA}},
		hosted:  map[[2][16]byte]bool{{engA.Bytes, reader.UserID.Bytes}: true},
	})
}

func TestConnection(t *testing.T) {
	t.Parallel()
	c := fixture()
	ctx := context.Background()
	cases := []struct {
		name string
		p    Principal
		conn pgtype.UUID
		want error
	}{
		{"owner", owner, connA, nil},
		{"global admin", admin, connA, nil},
		{"foreign user is told it does not exist", other, connA, ErrNotFound},
		{"missing connection", owner, uid(99), ErrNotFound},
		{"legacy row without created_by matches nobody", owner, connNo, ErrNotFound},
		{"legacy row is still reachable by an admin", admin, connNo, nil},
	}
	for _, tc := range cases {
		for _, act := range []Action{ActRead, ActWrite, ActAdmin} {
			if _, err := c.Connection(ctx, tc.p, tc.conn, act); !errors.Is(err, tc.want) {
				t.Errorf("%s (act %d): err = %v, want %v", tc.name, act, err, tc.want)
			}
		}
	}
}

func TestCatalog(t *testing.T) {
	t.Parallel()
	c := fixture()
	ctx := context.Background()
	if cat, conn, err := c.Catalog(ctx, owner, catA, ActWrite); err != nil || cat.ID != catA || conn.ID != connA {
		t.Fatalf("owner: %v %v %v", cat.ID, conn.ID, err)
	}
	if _, _, err := c.Catalog(ctx, admin, catA, ActWrite); err != nil {
		t.Fatalf("admin: %v", err)
	}
	if _, _, err := c.Catalog(ctx, other, catA, ActRead); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign: %v, want ErrNotFound", err)
	}
	if _, _, err := c.Catalog(ctx, owner, uid(98), ActRead); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing catalog: %v", err)
	}
}

// TestCatalogGrantMember: a member of a workspace granted the catalog may read
// it, may not change it (403, they can already see it), and does not reach the
// connection itself.
func TestCatalogGrantMember(t *testing.T) {
	t.Parallel()
	c := fixture()
	ctx := context.Background()
	if _, _, err := c.Catalog(ctx, reader, catA, ActRead); err != nil {
		t.Fatalf("grant member read: %v", err)
	}
	for _, act := range []Action{ActWrite, ActAdmin} {
		if _, _, err := c.Catalog(ctx, reader, catA, act); !errors.Is(err, ErrForbidden) {
			t.Fatalf("grant member act %d: %v, want ErrForbidden", act, err)
		}
	}
	for _, act := range []Action{ActRead, ActWrite, ActAdmin} {
		if _, err := c.Connection(ctx, reader, connA, act); !errors.Is(err, ErrNotFound) {
			t.Fatalf("grant member on the connection (act %d): %v, want ErrNotFound", act, err)
		}
	}
	if _, _, err := c.CatalogOfConnection(ctx, reader, connA, catA, ActRead); err != nil {
		t.Fatalf("grant member read through its connection: %v", err)
	}
}

func TestWorkspace(t *testing.T) {
	t.Parallel()
	c := fixture()
	ctx := context.Background()
	cases := []struct {
		name string
		p    Principal
		ws   pgtype.UUID
		act  Action
		want error
	}{
		{"member reads", reader, wsX, ActRead, nil},
		{"member may not write", reader, wsX, ActWrite, ErrForbidden},
		{"owner writes", wsOwn, wsX, ActAdmin, nil},
		{"admin writes", admin, wsX, ActAdmin, nil},
		{"stranger is told it does not exist", other, wsX, ActRead, ErrNotFound},
		{"stranger write is 404 too", other, wsX, ActWrite, ErrNotFound},
		{"missing workspace", owner, uid(97), ActRead, ErrNotFound},
	}
	for _, tc := range cases {
		if _, err := c.Workspace(ctx, tc.p, tc.ws, tc.act); !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v, want %v", tc.name, err, tc.want)
		}
	}
}

func TestTeam(t *testing.T) {
	t.Parallel()
	c := fixture()
	ctx := context.Background()
	cases := []struct {
		name string
		p    Principal
		act  Action
		want error
	}{
		{"member reads", reader, ActRead, nil},
		{"member may not write", reader, ActWrite, ErrForbidden},
		{"owner writes", wsOwn, ActWrite, nil},
		{"admin writes", admin, ActWrite, nil},
		{"stranger is told it does not exist", other, ActRead, ErrNotFound},
	}
	for _, tc := range cases {
		if _, err := c.Team(ctx, tc.p, teamT, tc.act); !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v, want %v", tc.name, err, tc.want)
		}
	}
	if _, err := c.Team(ctx, owner, uid(96), ActRead); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing team: %v", err)
	}
}

// TestCatalogOfConnection: a catalog id must be reached through its own
// connection, so a connection you own cannot be used to reach another's catalog.
func TestCatalogOfConnection(t *testing.T) {
	t.Parallel()
	c := fixture()
	ctx := context.Background()
	if _, _, err := c.CatalogOfConnection(ctx, owner, connA, catA, ActRead); err != nil {
		t.Fatalf("matching connection: %v", err)
	}
	// 'other' owns connB, but catA belongs to connA.
	if _, _, err := c.CatalogOfConnection(ctx, other, connB, catA, ActRead); !errors.Is(err, ErrNotFound) {
		t.Fatalf("catalog of another connection under an owned connection: %v, want ErrNotFound", err)
	}
	// Even an admin cannot address a catalog through the wrong connection.
	if _, _, err := c.CatalogOfConnection(ctx, admin, connB, catA, ActRead); !errors.Is(err, ErrNotFound) {
		t.Fatalf("admin with mismatched connection: %v, want ErrNotFound", err)
	}
}

func TestRequireAdmin(t *testing.T) {
	t.Parallel()
	if err := admin.RequireAdmin(); err != nil {
		t.Fatal(err)
	}
	if err := owner.RequireAdmin(); !errors.Is(err, ErrForbidden) {
		t.Fatalf("member: %v, want ErrForbidden", err)
	}
}

func TestPrincipalFromContext(t *testing.T) {
	t.Parallel()
	ctx := context.WithValue(context.Background(), auth.ContextKeyUserID, "11111111-1111-1111-1111-111111111111")
	ctx = context.WithValue(ctx, auth.ContextKeyRole, "admin")
	p, err := PrincipalFromContext(ctx)
	if err != nil || !p.IsAdmin() || !p.UserID.Valid {
		t.Fatalf("PrincipalFromContext = %+v, %v", p, err)
	}
	for _, bad := range []context.Context{
		context.Background(),
		context.WithValue(context.Background(), auth.ContextKeyUserID, "not-a-uuid"),
	} {
		if _, err := PrincipalFromContext(bad); !errors.Is(err, ErrUnauthenticated) {
			t.Fatalf("bad context: %v, want ErrUnauthenticated", err)
		}
	}
}

func TestEngine(t *testing.T) {
	t.Parallel()
	c := fixture()
	ctx := context.Background()
	cases := []struct {
		name string
		p    Principal
		act  Action
		want error
	}{
		{"connection owner may do anything", owner, ActAdmin, nil},
		{"global admin may do anything", admin, ActAdmin, nil},
		{"a grantee may read", reader, ActRead, nil},
		{"a grantee may not write", reader, ActWrite, ErrForbidden},
		{"a stranger is told it does not exist", other, ActRead, ErrNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, _, err := c.Engine(ctx, tc.p, engA, tc.act)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
	if _, _, err := c.Engine(ctx, admin, uid(99), ActRead); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing engine: %v", err)
	}
}

// TestDenialHook: every refusal is reported once, by resource and reason;
// allowed calls report nothing.
func TestDenialHook(t *testing.T) {
	t.Parallel()
	var got []string
	c := fixture().WithDenials(func(resource, reason string) { got = append(got, resource+":"+reason) })
	ctx := context.Background()
	_, _ = c.Connection(ctx, owner, connA, ActRead)                   // allowed
	_, _ = c.Connection(ctx, other, connA, ActRead)                   // not_found
	_, _, _ = c.Catalog(ctx, reader, catA, ActWrite)                  // forbidden: a reader cannot write
	_, _, _ = c.CatalogOfConnection(ctx, owner, connB, catA, ActRead) // wrong connection: not_found
	_, _ = c.Workspace(ctx, other, wsX, ActRead)                      // not_found
	want := []string{"connection:not_found", "catalog:forbidden", "catalog:not_found", "workspace:not_found"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("denials = %v, want %v", got, want)
	}
}

// TestAgentWorkspaceBinding: an agent reaches a workspace only while it is
// bound to it and its owner is still a member (#59).
func TestAgentWorkspaceBinding(t *testing.T) {
	t.Parallel()
	agentID := uid(60)
	c := New(store{
		wss:        map[[16]byte]db.Workspace{wsX.Bytes: {ID: wsX}},
		members:    map[[2][16]byte]bool{{wsX.Bytes, owner.UserID.Bytes}: true},
		agentBound: map[[2][16]byte]bool{{agentID.Bytes, wsX.Bytes}: true},
	})
	ctx := context.Background()
	agent := Principal{UserID: owner.UserID, Role: "member", Kind: KindAgent, AgentID: agentID}
	if _, err := c.Workspace(ctx, agent, wsX, ActRead); err != nil {
		t.Fatalf("bound agent: %v", err)
	}

	unbound := Principal{UserID: owner.UserID, Role: "member", Kind: KindAgent, AgentID: uid(61)}
	if _, err := c.Workspace(ctx, unbound, wsX, ActRead); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unbound agent = %v, want ErrNotFound", err)
	}

	// The owner leaving the workspace closes the agent's access too.
	c = New(store{
		wss:        map[[16]byte]db.Workspace{wsX.Bytes: {ID: wsX}},
		agentBound: map[[2][16]byte]bool{{agentID.Bytes, wsX.Bytes}: true},
	})
	if _, err := c.Workspace(ctx, agent, wsX, ActRead); !errors.Is(err, ErrNotFound) {
		t.Fatalf("owner removed = %v, want ErrNotFound", err)
	}
}

// TestAgentDeniedAdminActions: an agent never carries the owner's global role
// and every write is refused, whatever the owner may do (#59).
func TestAgentDeniedAdminActions(t *testing.T) {
	t.Parallel()
	c := fixture()
	ctx := context.Background()
	agent := Principal{UserID: owner.UserID, Role: "member", Kind: KindAgent, AgentID: uid(60)}

	if _, err := c.Connection(ctx, agent, connA, ActWrite); !errors.Is(err, ErrNotFound) {
		t.Errorf("connection read/write = %v, want ErrNotFound", err)
	}
	if _, _, err := c.Catalog(ctx, agent, catA, ActWrite); !errors.Is(err, ErrForbidden) {
		t.Errorf("catalog write = %v, want ErrForbidden", err)
	}
	if _, _, err := c.Engine(ctx, agent, engA, ActWrite); !errors.Is(err, ErrForbidden) {
		t.Errorf("engine write = %v, want ErrForbidden", err)
	}
	if _, err := c.Workspace(ctx, agent, wsX, ActWrite); !errors.Is(err, ErrForbidden) {
		t.Errorf("workspace write = %v, want ErrForbidden", err)
	}
	if _, err := c.Team(ctx, agent, teamT, ActWrite); !errors.Is(err, ErrForbidden) {
		t.Errorf("team write = %v, want ErrForbidden", err)
	}
	if err := agent.RequireAdmin(); !errors.Is(err, ErrForbidden) {
		t.Errorf("RequireAdmin = %v, want ErrForbidden", err)
	}
}

// TestAgentResourceScope: an agent reaches catalogs and engines only through a
// workspace it is bound to; the owner's connection ownership and grants in
// other workspaces do not widen that scope (#59).
func TestAgentResourceScope(t *testing.T) {
	t.Parallel()
	agentID := uid(60)
	catB := uid(21)
	c := New(store{
		conns: map[[16]byte]db.CatalogConnection{connA.Bytes: {ID: connA, CreatedBy: owner.UserID}},
		cats: map[[16]byte]db.Catalog{
			catA.Bytes: {ID: catA, ConnectionID: connA},
			catB.Bytes: {ID: catB, ConnectionID: connA},
		},
		engines:               map[[16]byte]db.Engine{engA.Bytes: {ID: engA, ConnectionID: connA}},
		agentBound:            map[[2][16]byte]bool{{agentID.Bytes, wsX.Bytes}: true},
		catalogGrantedToAgent: map[[2][16]byte]bool{{catB.Bytes, agentID.Bytes}: true},
		engineHostsForAgent:   map[[2][16]byte]bool{{engA.Bytes, agentID.Bytes}: true},
	})
	ctx := context.Background()
	agent := Principal{UserID: owner.UserID, Role: "member", Kind: KindAgent, AgentID: agentID}

	if _, _, err := c.Catalog(ctx, agent, catB, ActRead); err != nil {
		t.Fatalf("catalog granted to a bound workspace: %v", err)
	}
	// catA is under a connection the owner created, but it is not granted to a
	// workspace the agent is bound to.
	if _, _, err := c.Catalog(ctx, agent, catA, ActRead); !errors.Is(err, ErrNotFound) {
		t.Errorf("ungranted catalog = %v, want ErrNotFound", err)
	}
	if _, _, err := c.Engine(ctx, agent, engA, ActRead); err != nil {
		t.Fatalf("engine hosting a granted catalog: %v", err)
	}
	if _, err := c.Connection(ctx, agent, connA, ActRead); !errors.Is(err, ErrNotFound) {
		t.Errorf("connection read = %v, want ErrNotFound", err)
	}
}
