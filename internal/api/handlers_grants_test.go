package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// grantsState is the workspace/team/grant side of the authz fake.
type grantsState struct {
	mu     sync.Mutex
	teams  map[[16]byte]db.Team
	roles  map[[2][16]byte]string // {team, user} -> team role
	wss    map[[16]byte]db.Workspace
	grants []db.WorkspaceCatalogGrant
	writes int // grant upserts and deletes that reached the store
}

func uuidOf(s string) pgtype.UUID {
	u, err := uuidFromString(s)
	if err != nil {
		panic(err)
	}
	return u
}

func (q *authzFake) isWsMember(ws db.Workspace, user pgtype.UUID) bool {
	if ws.OwnerUserID.Valid && ws.OwnerUserID.Bytes == user.Bytes {
		return true
	}
	if ws.OwnerTeamID.Valid {
		_, ok := q.g.roles[[2][16]byte{ws.OwnerTeamID.Bytes, user.Bytes}]
		return ok
	}
	return false
}

func (q *authzFake) CatalogGrantedToUser(_ context.Context, a db.CatalogGrantedToUserParams) (bool, error) {
	q.g.mu.Lock()
	defer q.g.mu.Unlock()
	for _, g := range q.g.grants {
		if g.CatalogID.Bytes == a.CatalogID.Bytes && q.isWsMember(q.g.wss[g.WorkspaceID.Bytes], a.UserID) {
			return true, nil
		}
	}
	return false, nil
}

func (q *authzFake) ListCatalogsGrantedToUser(ctx context.Context, a db.ListCatalogsGrantedToUserParams) ([]db.Catalog, error) {
	if a.ConnectionID.Bytes != q.cat.ConnectionID.Bytes {
		return nil, nil
	}
	ok, _ := q.CatalogGrantedToUser(ctx, db.CatalogGrantedToUserParams{CatalogID: q.cat.ID, UserID: a.UserID})
	if !ok {
		return nil, nil
	}
	return []db.Catalog{q.cat}, nil
}

func (q *authzFake) GetWorkspaceByID(_ context.Context, id pgtype.UUID) (db.Workspace, error) {
	q.g.mu.Lock()
	defer q.g.mu.Unlock()
	if w, ok := q.g.wss[id.Bytes]; ok {
		return w, nil
	}
	return db.Workspace{}, pgx.ErrNoRows
}

func (q *authzFake) CheckWorkspaceAccess(_ context.Context, a db.CheckWorkspaceAccessParams) (bool, error) {
	q.g.mu.Lock()
	defer q.g.mu.Unlock()
	w, ok := q.g.wss[a.ID.Bytes]
	return ok && q.isWsMember(w, a.UserID), nil
}

func (q *authzFake) IsWorkspaceOwner(_ context.Context, a db.IsWorkspaceOwnerParams) (bool, error) {
	q.g.mu.Lock()
	defer q.g.mu.Unlock()
	w, ok := q.g.wss[a.ID.Bytes]
	if !ok {
		return false, nil
	}
	if w.OwnerUserID.Valid && w.OwnerUserID.Bytes == a.UserID.Bytes {
		return true, nil
	}
	return w.OwnerTeamID.Valid && q.g.roles[[2][16]byte{w.OwnerTeamID.Bytes, a.UserID.Bytes}] == "owner", nil
}

func (q *authzFake) GetTeamByID(_ context.Context, id pgtype.UUID) (db.Team, error) {
	if t, ok := q.g.teams[id.Bytes]; ok {
		return t, nil
	}
	return db.Team{}, pgx.ErrNoRows
}

func (q *authzFake) GetTeamMemberRole(_ context.Context, a db.GetTeamMemberRoleParams) (string, error) {
	if r, ok := q.g.roles[[2][16]byte{a.TeamID.Bytes, a.UserID.Bytes}]; ok {
		return r, nil
	}
	return "", pgx.ErrNoRows
}

func (q *authzFake) ListTeams(context.Context) ([]db.Team, error) {
	out := []db.Team{}
	for _, t := range q.g.teams {
		out = append(out, t)
	}
	return out, nil
}

func (q *authzFake) ListTeamsByUser(_ context.Context, user pgtype.UUID) ([]db.Team, error) {
	out := []db.Team{}
	for k, t := range q.g.teams {
		if _, ok := q.g.roles[[2][16]byte{k, user.Bytes}]; ok {
			out = append(out, t)
		}
	}
	return out, nil
}

func (q *authzFake) UpdateWorkspace(_ context.Context, p db.UpdateWorkspaceParams) (db.Workspace, error) {
	q.g.mu.Lock()
	defer q.g.mu.Unlock()
	w := q.g.wss[p.ID.Bytes]
	w.Name, w.Description = p.Name, p.Description
	q.order = append(q.order, "update-workspace")
	return w, nil
}

func (q *authzFake) UpsertWorkspaceCatalogGrant(_ context.Context, p db.UpsertWorkspaceCatalogGrantParams) (db.WorkspaceCatalogGrant, error) {
	q.g.mu.Lock()
	defer q.g.mu.Unlock()
	q.g.writes++
	g := db.WorkspaceCatalogGrant{WorkspaceID: p.WorkspaceID, CatalogID: p.CatalogID, Permission: p.Permission, GrantedBy: p.GrantedBy}
	for i, e := range q.g.grants {
		if e.WorkspaceID.Bytes == p.WorkspaceID.Bytes && e.CatalogID.Bytes == p.CatalogID.Bytes {
			q.g.grants[i] = g
			return g, nil
		}
	}
	q.g.grants = append(q.g.grants, g)
	return g, nil
}

func (q *authzFake) ListWorkspaceCatalogGrants(_ context.Context, ws pgtype.UUID) ([]db.ListWorkspaceCatalogGrantsRow, error) {
	q.g.mu.Lock()
	defer q.g.mu.Unlock()
	out := []db.ListWorkspaceCatalogGrantsRow{}
	for _, g := range q.g.grants {
		if g.WorkspaceID.Bytes == ws.Bytes {
			out = append(out, db.ListWorkspaceCatalogGrantsRow{WorkspaceID: g.WorkspaceID, CatalogID: g.CatalogID, Permission: g.Permission, CatalogName: q.cat.TrinoCatalogName})
		}
	}
	return out, nil
}

func (q *authzFake) DeleteWorkspaceCatalogGrant(_ context.Context, p db.DeleteWorkspaceCatalogGrantParams) (int64, error) {
	q.g.mu.Lock()
	defer q.g.mu.Unlock()
	q.g.writes++
	for i, e := range q.g.grants {
		if e.WorkspaceID.Bytes == p.WorkspaceID.Bytes && e.CatalogID.Bytes == p.CatalogID.Bytes {
			q.g.grants = append(q.g.grants[:i], q.g.grants[i+1:]...)
			return 1, nil
		}
	}
	return 0, nil
}

func (q *authzFake) ListGrantedCatalogsOfWorkspace(_ context.Context, ws pgtype.UUID) ([]db.ListGrantedCatalogsOfWorkspaceRow, error) {
	q.g.mu.Lock()
	defer q.g.mu.Unlock()
	out := []db.ListGrantedCatalogsOfWorkspaceRow{}
	for _, g := range q.g.grants {
		if g.WorkspaceID.Bytes == ws.Bytes {
			out = append(out, db.ListGrantedCatalogsOfWorkspaceRow{
				ID: q.cat.ID, ConnectionID: q.cat.ConnectionID, TrinoCatalogName: q.cat.TrinoCatalogName,
				ConnectionName: q.conn.Name, ConnectionType: q.conn.Type,
			})
		}
	}
	return out, nil
}

func (q *authzFake) ListCatalogConnections(context.Context, pgtype.UUID) ([]db.CatalogConnection, error) {
	return nil, nil
}

const (
	memberID = "44444444-4444-4444-4444-444444444444" // plain member of the team
)

var (
	teamID = pgtype.UUID{Bytes: [16]byte{0x7E}, Valid: true}
	wsID   = pgtype.UUID{Bytes: [16]byte{0x5A}, Valid: true}
)

// newGrantsHandlers builds on newAuthzHandlers: a team where ownerID is the team
// owner (and owns the connection) and memberID is a plain member, and a shared
// workspace owned by that team.
func newGrantsHandlers(t *testing.T) (*Handlers, *authzFake) {
	t.Helper()
	h, q, _ := newAuthzHandlers(t)
	q.g.teams = map[[16]byte]db.Team{teamID.Bytes: {ID: teamID, Name: "data"}}
	q.g.roles = map[[2][16]byte]string{
		{teamID.Bytes, uuidOf(ownerID).Bytes}:  "owner",
		{teamID.Bytes, uuidOf(memberID).Bytes}: "member",
	}
	q.g.wss = map[[16]byte]db.Workspace{wsID.Bytes: {ID: wsID, Name: "shared", Type: "shared", OwnerTeamID: teamID}}
	return h, q
}

func grantBody(q *authzFake, perm GrantCatalogAccessRequestPermission) *GrantCatalogAccessJSONRequestBody {
	return &GrantCatalogAccessJSONRequestBody{CatalogId: q.cat.ID.Bytes, Permission: perm}
}

func wsCode(t *testing.T, resp interface{}) int {
	t.Helper()
	rec := httptest.NewRecorder()
	switch r := resp.(type) {
	case interface {
		VisitGrantCatalogAccessResponse(http.ResponseWriter) error
	}:
		_ = r.VisitGrantCatalogAccessResponse(rec)
	case interface {
		VisitRevokeCatalogAccessResponse(http.ResponseWriter) error
	}:
		_ = r.VisitRevokeCatalogAccessResponse(rec)
	case interface {
		VisitListWorkspaceCatalogAccessResponse(http.ResponseWriter) error
	}:
		_ = r.VisitListWorkspaceCatalogAccessResponse(rec)
	case interface {
		VisitUpdateWorkspaceResponse(http.ResponseWriter) error
	}:
		_ = r.VisitUpdateWorkspaceResponse(rec)
	case interface {
		VisitGetTeamResponse(http.ResponseWriter) error
	}:
		_ = r.VisitGetTeamResponse(rec)
	case interface {
		VisitListTeamMembersResponse(http.ResponseWriter) error
	}:
		_ = r.VisitListTeamMembersResponse(rec)
	case interface {
		VisitListTeamsResponse(http.ResponseWriter) error
	}:
		_ = r.VisitListTeamsResponse(rec)
	default:
		t.Fatalf("unhandled response type %T", resp)
	}
	return rec.Code
}

func grant(t *testing.T, h *Handlers, q *authzFake, ctx context.Context, perm GrantCatalogAccessRequestPermission) int {
	t.Helper()
	resp, err := h.GrantCatalogAccess(ctx, GrantCatalogAccessRequestObject{Id: wsID.Bytes, Body: grantBody(q, perm)})
	if err != nil {
		t.Fatal(err)
	}
	return wsCode(t, resp)
}

// TestGrantCatalogAccess_Persists: grant -> list shows it -> revoke -> gone.
func TestGrantCatalogAccess_Persists(t *testing.T) {
	h, q := newGrantsHandlers(t)
	owner := ctxAs(ownerID, "member")

	if got := grant(t, h, q, owner, GrantCatalogAccessRequestPermissionRead); got != 201 {
		t.Fatalf("grant: %d, want 201", got)
	}
	list, err := h.ListWorkspaceCatalogAccess(owner, ListWorkspaceCatalogAccessRequestObject{Id: wsID.Bytes})
	if err != nil {
		t.Fatal(err)
	}
	rows, ok := list.(ListWorkspaceCatalogAccess200JSONResponse)
	if !ok || len(rows) != 1 || rows[0].Permission != WorkspaceCatalogAccessPermissionRead || rows[0].CatalogId != q.cat.ID.Bytes {
		t.Fatalf("list after grant = %#v", list)
	}

	// Granting again changes the permission instead of failing.
	if got := grant(t, h, q, owner, GrantCatalogAccessRequestPermissionWrite); got != 201 {
		t.Fatalf("re-grant: %d", got)
	}
	list, _ = h.ListWorkspaceCatalogAccess(owner, ListWorkspaceCatalogAccessRequestObject{Id: wsID.Bytes})
	if rows := list.(ListWorkspaceCatalogAccess200JSONResponse); len(rows) != 1 || rows[0].Permission != WorkspaceCatalogAccessPermissionWrite {
		t.Fatalf("list after re-grant = %#v", rows)
	}

	resp, err := h.RevokeCatalogAccess(owner, RevokeCatalogAccessRequestObject{Id: wsID.Bytes, CatalogId: q.cat.ID.Bytes})
	if err != nil || wsCode(t, resp) != 204 {
		t.Fatalf("revoke: %v %v", resp, err)
	}
	list, _ = h.ListWorkspaceCatalogAccess(owner, ListWorkspaceCatalogAccessRequestObject{Id: wsID.Bytes})
	if rows := list.(ListWorkspaceCatalogAccess200JSONResponse); len(rows) != 0 {
		t.Fatalf("list after revoke = %#v", rows)
	}
	// Revoking what is not granted is a 404, not a silent 204.
	resp, _ = h.RevokeCatalogAccess(owner, RevokeCatalogAccessRequestObject{Id: wsID.Bytes, CatalogId: q.cat.ID.Bytes})
	if got := wsCode(t, resp); got != 404 {
		t.Fatalf("revoke twice: %d, want 404", got)
	}
}

// TestGrantCatalogAccess_Authz: both parties must agree and nothing is written
// before the checks pass.
func TestGrantCatalogAccess_Authz(t *testing.T) {
	cases := []struct {
		name string
		ctx  context.Context
		want int
	}{
		{"conn-owner and team owner", ctxAs(ownerID, "member"), 201},
		{"global admin", ctxAs(adminID, "admin"), 201},
		{"workspace member, not an owner", ctxAs(memberID, "member"), 403},
		{"stranger", ctxAs(strangerID, "member"), 404},
	}
	for _, tc := range cases {
		h, q := newGrantsHandlers(t)
		if got := grant(t, h, q, tc.ctx, GrantCatalogAccessRequestPermissionRead); got != tc.want {
			t.Errorf("%s: %d, want %d", tc.name, got, tc.want)
		}
		if tc.want != 201 && q.g.writes != 0 {
			t.Errorf("%s: a denied grant reached the store (%d writes)", tc.name, q.g.writes)
		}
	}

	// A workspace owner who does not own the catalog's connection cannot grant it.
	h, q := newGrantsHandlers(t)
	q.g.roles[[2][16]byte{teamID.Bytes, uuidOf(strangerID).Bytes}] = "owner"
	if got := grant(t, h, q, ctxAs(strangerID, "member"), GrantCatalogAccessRequestPermissionRead); got != 404 {
		t.Errorf("team owner without the catalog: %d, want 404", got)
	}
	if q.g.writes != 0 {
		t.Errorf("denied grant reached the store")
	}

	// An unknown permission is rejected after authorization and before any write.
	h, q = newGrantsHandlers(t)
	if got := grant(t, h, q, ctxAs(ownerID, "member"), GrantCatalogAccessRequestPermission("admin")); got != 422 {
		t.Errorf("bad permission: %d, want 422", got)
	}
	if q.g.writes != 0 {
		t.Errorf("invalid grant reached the store")
	}
}

func TestRevokeCatalogAccess_Authz(t *testing.T) {
	for _, tc := range []struct {
		name string
		ctx  context.Context
		want int
	}{
		{"member", ctxAs(memberID, "member"), 403},
		{"stranger", ctxAs(strangerID, "member"), 404},
		{"owner", ctxAs(ownerID, "member"), 204},
	} {
		h, q := newGrantsHandlers(t)
		if got := grant(t, h, q, ctxAs(ownerID, "member"), GrantCatalogAccessRequestPermissionRead); got != 201 {
			t.Fatalf("setup grant: %d", got)
		}
		before := q.g.writes
		resp, err := h.RevokeCatalogAccess(tc.ctx, RevokeCatalogAccessRequestObject{Id: wsID.Bytes, CatalogId: q.cat.ID.Bytes})
		if err != nil {
			t.Fatal(err)
		}
		if got := wsCode(t, resp); got != tc.want {
			t.Errorf("%s: %d, want %d", tc.name, got, tc.want)
		}
		if tc.want != 204 && q.g.writes != before {
			t.Errorf("%s: a denied revoke reached the store", tc.name)
		}
	}
}

func TestListWorkspaceCatalogAccess_Authz(t *testing.T) {
	h, q := newGrantsHandlers(t)
	_ = grant(t, h, q, ctxAs(ownerID, "member"), GrantCatalogAccessRequestPermissionRead)
	for name, tc := range map[string]struct {
		ctx  context.Context
		want int
	}{"member": {ctxAs(memberID, "member"), 200}, "owner": {ctxAs(ownerID, "member"), 200}, "stranger": {ctxAs(strangerID, "member"), 404}} {
		resp, err := h.ListWorkspaceCatalogAccess(tc.ctx, ListWorkspaceCatalogAccessRequestObject{Id: wsID.Bytes})
		if err != nil {
			t.Fatal(err)
		}
		if got := wsCode(t, resp); got != tc.want {
			t.Errorf("%s: %d, want %d", name, got, tc.want)
		}
	}
}

// TestGrantMemberCatalogAccess: a workspace member reaches a granted catalog
// read-only; when the grant is revoked the catalog is a 404 again.
func TestGrantMemberCatalogAccess(t *testing.T) {
	h, q := newGrantsHandlers(t)
	member := ctxAs(memberID, "member")
	conn, cat := reqIDs(q)

	get := func() int {
		resp, err := h.GetCatalog(member, GetCatalogRequestObject{ConnectionId: conn, CatalogId: cat})
		if err != nil {
			t.Fatal(err)
		}
		return visitCode(t, resp)
	}
	list := func() int {
		resp, err := h.ListCatalogs(member, ListCatalogsRequestObject{ConnectionId: conn})
		if err != nil {
			t.Fatal(err)
		}
		return visitCode(t, resp)
	}
	if get() != 404 || list() != 404 {
		t.Fatal("member must not reach the catalog before it is granted")
	}
	_ = grant(t, h, q, ctxAs(ownerID, "member"), GrantCatalogAccessRequestPermissionRead)
	if got := get(); got != 200 {
		t.Errorf("granted get: %d, want 200", got)
	}
	if got := list(); got != 200 {
		t.Errorf("granted list: %d, want 200", got)
	}
	// Read access is not write access: the member can see the catalog, so 403.
	upd, err := h.UpdateCatalog(member, UpdateCatalogRequestObject{ConnectionId: conn, CatalogId: cat, Body: &UpdateCatalogJSONRequestBody{}})
	if err != nil {
		t.Fatal(err)
	}
	if got := visitCode(t, upd); got != 403 {
		t.Errorf("granted update: %d, want 403", got)
	}
	// The connection itself stays private.
	cr, err := h.GetCatalogConnection(member, GetCatalogConnectionRequestObject{Id: conn})
	if err != nil {
		t.Fatal(err)
	}
	if got := visitConnCode(t, cr); got != 404 {
		t.Errorf("granted member on the connection: %d, want 404", got)
	}
	_, _ = h.RevokeCatalogAccess(ctxAs(ownerID, "member"), RevokeCatalogAccessRequestObject{Id: wsID.Bytes, CatalogId: cat})
	if get() != 404 {
		t.Error("member still reaches the catalog after revoke")
	}
}

func visitConnCode(t *testing.T, resp interface{}) int {
	t.Helper()
	rec := httptest.NewRecorder()
	r, ok := resp.(interface {
		VisitGetCatalogConnectionResponse(http.ResponseWriter) error
	})
	if !ok {
		t.Fatalf("unhandled response type %T", resp)
	}
	_ = r.VisitGetCatalogConnectionResponse(rec)
	return rec.Code
}

func TestUpdateWorkspace_OwnerRule(t *testing.T) {
	for _, tc := range []struct {
		name string
		ctx  context.Context
		want int
	}{
		{"team owner", ctxAs(ownerID, "member"), 200},
		{"admin", ctxAs(adminID, "admin"), 200},
		{"member renames shared workspace", ctxAs(memberID, "member"), 403},
		{"stranger", ctxAs(strangerID, "member"), 404},
	} {
		h, q := newGrantsHandlers(t)
		name := "renamed"
		resp, err := h.UpdateWorkspace(tc.ctx, UpdateWorkspaceRequestObject{Id: wsID.Bytes, Body: &UpdateWorkspaceJSONRequestBody{Name: &name}})
		if err != nil {
			t.Fatal(err)
		}
		if got := wsCode(t, resp); got != tc.want {
			t.Errorf("%s: %d, want %d", tc.name, got, tc.want)
		}
		wrote := false
		for _, o := range q.order {
			wrote = wrote || o == "update-workspace"
		}
		if wrote != (tc.want == 200) {
			t.Errorf("%s: workspace written = %v", tc.name, wrote)
		}
	}
}

func TestTeamsReadScoping(t *testing.T) {
	h, q := newGrantsHandlers(t)
	other := db.Team{ID: pgtype.UUID{Bytes: [16]byte{0x7F}, Valid: true}, Name: "other"}
	q.g.teams[other.ID.Bytes] = other

	n := func(ctx context.Context) int {
		resp, err := h.ListTeams(ctx, ListTeamsRequestObject{})
		if err != nil {
			t.Fatal(err)
		}
		return len(resp.(ListTeams200JSONResponse))
	}
	if got := n(ctxAs(memberID, "member")); got != 1 {
		t.Errorf("member sees %d teams, want 1", got)
	}
	if got := n(ctxAs(strangerID, "member")); got != 0 {
		t.Errorf("stranger sees %d teams, want 0", got)
	}
	if got := n(ctxAs(adminID, "admin")); got != 2 {
		t.Errorf("admin sees %d teams, want 2", got)
	}

	for name, tc := range map[string]struct {
		ctx  context.Context
		want int
	}{"member": {ctxAs(memberID, "member"), 200}, "admin": {ctxAs(adminID, "admin"), 200}, "stranger": {ctxAs(strangerID, "member"), 404}} {
		g, err := h.GetTeam(tc.ctx, GetTeamRequestObject{Id: teamID.Bytes})
		if err != nil {
			t.Fatal(err)
		}
		if got := wsCode(t, g); got != tc.want {
			t.Errorf("GetTeam %s: %d, want %d", name, got, tc.want)
		}
		m, err := h.ListTeamMembers(tc.ctx, ListTeamMembersRequestObject{Id: teamID.Bytes})
		if err != nil {
			t.Fatal(err)
		}
		if got := wsCode(t, m); got != tc.want {
			t.Errorf("ListTeamMembers %s: %d, want %d", name, got, tc.want)
		}
	}
}

// TestListWorkspaceCatalogs_GrantsAndCache: a member sees granted catalogs, a
// stranger gets a 404 (not a 403), and a revoke is visible immediately even
// though the response is cached.
func TestListWorkspaceCatalogs_GrantsAndCache(t *testing.T) {
	h, q := newGrantsHandlers(t)

	listAs := func(user string) (int, []workspaceCatalogEntry) {
		req := httptest.NewRequest(http.MethodGet, "/workspaces/x/catalogs", nil)
		rc := chi.NewRouteContext()
		rc.URLParams.Add("id", uuidString(wsID))
		req = req.WithContext(context.WithValue(ctxAs(user, "member"), chi.RouteCtxKey, rc))
		rec := httptest.NewRecorder()
		h.ListWorkspaceCatalogs(rec, req)
		var out []workspaceCatalogEntry
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}

	if code, out := listAs(memberID); code != 200 || len(out) != 0 {
		t.Fatalf("before grant: %d %v", code, out)
	}
	if code, _ := listAs(strangerID); code != 404 {
		t.Fatalf("stranger: %d, want 404", code)
	}
	_ = grant(t, h, q, ctxAs(ownerID, "member"), GrantCatalogAccessRequestPermissionRead)
	if code, out := listAs(memberID); code != 200 || len(out) != 1 || out[0].CatalogName != q.cat.TrinoCatalogName {
		t.Fatalf("after grant: %d %v", code, out)
	}
	_, _ = h.RevokeCatalogAccess(ctxAs(ownerID, "member"), RevokeCatalogAccessRequestObject{Id: wsID.Bytes, CatalogId: q.cat.ID.Bytes})
	if code, out := listAs(memberID); code != 200 || len(out) != 0 {
		t.Fatalf("after revoke the cached list is stale: %d %v", code, out)
	}
}

func (q *authzFake) ListTeamMembers(_ context.Context, team pgtype.UUID) ([]db.ListTeamMembersRow, error) {
	// Membership row ids differ from user ids on purpose, and the team role
	// ("owner") is not the user's global role.
	return []db.ListTeamMembersRow{
		{ID: pgtype.UUID{Bytes: [16]byte{0xA1}, Valid: true}, TeamID: team, UserID: uuidOf(ownerID), Role: "owner",
			UserName: "o", UserEmail: "o@x.io", UserRole: "admin", UserAuthProvider: "oidc"},
		{ID: pgtype.UUID{Bytes: [16]byte{0xA2}, Valid: true}, TeamID: team, UserID: uuidOf(memberID), Role: "member",
			UserName: "m", UserEmail: "m@x.io", UserRole: "member", UserAuthProvider: "local"},
	}, nil
}

// TestListTeamMembers_UserFields pins #150: user.id is the user's id (not the
// membership row's) and user.role / auth_provider come from the user.
func TestListTeamMembers_UserFields(t *testing.T) {
	h, _ := newGrantsHandlers(t)
	resp, err := h.ListTeamMembers(ctxAs(ownerID, "member"), ListTeamMembersRequestObject{Id: teamID.Bytes})
	if err != nil {
		t.Fatal(err)
	}
	members := resp.(ListTeamMembers200JSONResponse)
	if len(members) != 2 {
		t.Fatalf("members = %d", len(members))
	}
	first := members[0]
	if first.User.Id != uuidOf(ownerID).Bytes || first.Role != TeamMemberRoleOwner || first.User.Role != UserPublicRole("admin") || first.User.AuthProvider != "oidc" {
		t.Errorf("owner row = %+v", first)
	}
	second := members[1]
	if second.User.Id != uuidOf(memberID).Bytes || second.User.Role != UserPublicRole("member") {
		t.Errorf("member row = %+v", second)
	}
}
