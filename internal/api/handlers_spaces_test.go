package api

import (
	"context"
	"errors"
	"testing"

	"github.com/galileostudio/baleia/internal/catalogs/topology"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/store/model"
	"github.com/galileostudio/baleia/internal/store/repo"
	"github.com/galileostudio/baleia/internal/testdb"
	wsaccess "github.com/galileostudio/baleia/internal/workspaces/access"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

func oid(t *testing.T, s string) openapi_types.UUID {
	t.Helper()
	u, err := uuidFromString(s)
	if err != nil {
		t.Fatal(err)
	}
	return openapi_types.UUID(u.Bytes)
}

func defaultSpaceID(t *testing.T, f *metaFixture, ws openapi_types.UUID) openapi_types.UUID {
	t.Helper()
	var space model.Space
	if err := f.gdb.Where("workspace_id = ? AND is_default", repo.UUIDString(pgUUIDOf(ws))).First(&space).Error; err != nil {
		t.Fatal(err)
	}
	return openapi_types.UUID(repo.PGUUID(string(space.ID)).Bytes)
}

// sharedWorkspace adds a team (owner and stranger as members) and a shared
// workspace granted the fixture's catalog, so the fixture can test workspace
// members who are not the owner.
func sharedWorkspace(t *testing.T, f *metaFixture) openapi_types.UUID {
	t.Helper()
	owner, stranger := model.UUID(f.owner), model.UUID(f.stranger)
	team := model.Team{ID: model.UUID(repo.NewUUID()), Name: "t", CreatedBy: owner}
	if err := f.gdb.Create(&team).Error; err != nil {
		t.Fatal(err)
	}
	for _, m := range []struct {
		user model.UUID
		role model.Text
	}{{owner, "owner"}, {stranger, "member"}} {
		if err := f.gdb.Create(&model.TeamMember{
			ID: model.UUID(repo.NewUUID()), TeamID: team.ID, UserID: m.user, Role: m.role,
		}).Error; err != nil {
			t.Fatal(err)
		}
	}
	ws := model.Workspace{
		ID: model.UUID(repo.NewUUID()), Name: "shared", Type: "shared",
		PolarisCatalogName: "shared", OwnerTeamID: &team.ID,
	}
	if err := f.gdb.Create(&ws).Error; err != nil {
		t.Fatal(err)
	}
	addDefaultSpace(t, f.gdb, ws.ID)
	if _, err := repo.New(f.gdb).UpsertWorkspaceCatalogGrant(context.Background(), db.UpsertWorkspaceCatalogGrantParams{
		WorkspaceID: repo.PGUUID(string(ws.ID)), CatalogID: repo.PGUUID(repo.UUIDString(pgUUIDOf(f.lake))),
		GrantedBy: repo.PGUUID(f.owner),
	}); err != nil {
		t.Fatal(err)
	}
	return openapi_types.UUID(repo.PGUUID(string(ws.ID)).Bytes)
}

func createSpaceIn(t *testing.T, f *metaFixture, ctx context.Context, ws openapi_types.UUID, body CreateSpaceRequest) Space {
	t.Helper()
	r, err := f.h.CreateSpace(ctx, CreateSpaceRequestObject{Id: ws, Body: &body})
	if err != nil {
		t.Fatal(err)
	}
	created, ok := r.(CreateSpace201JSONResponse)
	if !ok {
		t.Fatalf("create space: %T", r)
	}
	return Space(created)
}

func createSpace(t *testing.T, f *metaFixture, ctx context.Context, body CreateSpaceRequest) Space {
	return createSpaceIn(t, f, ctx, f.ws, body)
}

func TestSpaces_CreateListGetUpdateDelete(t *testing.T) {
	t.Parallel()
	f := newMetaFixture(t)
	ctx := f.as(f.owner)

	space := createSpace(t, f, ctx, CreateSpaceRequest{
		Name: "Finance", Visibility: CreateSpaceRequestVisibilityWorkspace,
	})
	if space.Slug != "finance" || space.MyRole == nil || *space.MyRole != SpaceMyRoleManager {
		t.Fatalf("space = %+v", space)
	}

	list, err := f.h.ListSpaces(ctx, ListSpacesRequestObject{Id: f.ws})
	if err != nil {
		t.Fatal(err)
	}
	spaces := list.(ListSpaces200JSONResponse)
	if len(spaces) != 2 || !spaces[0].IsDefault || spaces[1].Id != space.Id {
		t.Fatalf("spaces = %+v", spaces)
	}

	got, err := f.h.GetSpace(ctx, GetSpaceRequestObject{Id: f.ws, SpaceId: space.Id})
	if err != nil {
		t.Fatal(err)
	}
	if g := got.(GetSpace200JSONResponse); g.Name != "Finance" || g.MyRole == nil || *g.MyRole != SpaceMyRoleManager {
		t.Fatalf("get space = %+v", g)
	}

	name := "Finance & Ops"
	up, err := f.h.UpdateSpace(ctx, UpdateSpaceRequestObject{
		Id: f.ws, SpaceId: space.Id, Body: &UpdateSpaceRequest{Name: &name},
	})
	if err != nil {
		t.Fatal(err)
	}
	if u := up.(UpdateSpace200JSONResponse); u.Name != name {
		t.Fatalf("update space = %+v", u)
	}

	if r, err := f.h.DeleteSpace(ctx, DeleteSpaceRequestObject{Id: f.ws, SpaceId: space.Id}); err != nil {
		t.Fatal(err)
	} else if _, ok := r.(DeleteSpace204Response); !ok {
		t.Fatalf("delete space: %T", r)
	}
	if r, err := f.h.GetSpace(ctx, GetSpaceRequestObject{Id: f.ws, SpaceId: space.Id}); err != nil {
		t.Fatal(err)
	} else if _, ok := r.(GetSpace404JSONResponse); !ok {
		t.Fatalf("get deleted space: %T", r)
	}
}

func TestSpaces_DefaultUndeletable(t *testing.T) {
	t.Parallel()
	f := newMetaFixture(t)
	ctx := f.as(f.owner)
	r, err := f.h.DeleteSpace(ctx, DeleteSpaceRequestObject{Id: f.ws, SpaceId: defaultSpaceID(t, f, f.ws)})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := r.(DeleteSpace409JSONResponse); !ok {
		t.Fatalf("delete default space: %T", r)
	}
}

func TestSpaces_RestrictedInvisibleAndViewer(t *testing.T) {
	t.Parallel()
	f := newMetaFixture(t)
	ws := sharedWorkspace(t, f)
	owner, member := f.as(f.owner), f.as(f.stranger)
	space := createSpaceIn(t, f, owner, ws, CreateSpaceRequest{
		Name: "Secret", Visibility: CreateSpaceRequestVisibilityRestricted,
	})

	// A workspace member without an entry does not see a restricted space.
	list, err := f.h.ListSpaces(member, ListSpacesRequestObject{Id: ws})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range list.(ListSpaces200JSONResponse) {
		if s.Id == space.Id {
			t.Fatalf("restricted space leaked to a member: %+v", s)
		}
	}
	if r, err := f.h.GetSpace(member, GetSpaceRequestObject{Id: ws, SpaceId: space.Id}); err != nil {
		t.Fatal(err)
	} else if _, ok := r.(GetSpace404JSONResponse); !ok {
		t.Fatalf("member get: %T", r)
	}

	// The manager grants the member a viewer entry: they can read, not write.
	if r, err := f.h.SetSpaceMember(owner, SetSpaceMemberRequestObject{
		Id: ws, SpaceId: space.Id, PrincipalType: "user", PrincipalId: oid(t, f.stranger),
		Body: &SetSpaceMemberJSONRequestBody{Role: SetSpaceMemberRequestRoleViewer},
	}); err != nil {
		t.Fatal(err)
	} else if _, ok := r.(SetSpaceMember200JSONResponse); !ok {
		t.Fatalf("set member: %T", r)
	}
	if r, err := f.h.GetSpace(member, GetSpaceRequestObject{Id: ws, SpaceId: space.Id}); err != nil {
		t.Fatal(err)
	} else if g, ok := r.(GetSpace200JSONResponse); !ok || g.MyRole == nil || *g.MyRole != SpaceMyRoleViewer {
		t.Fatalf("viewer get: %T %+v", r, g)
	}
	if r, err := f.h.CreateFolder(member, CreateFolderRequestObject{
		Id: ws, Body: &CreateFolderRequest{Name: "nope", SpaceId: &space.Id},
	}); err != nil {
		t.Fatal(err)
	} else if _, ok := r.(CreateFolder403JSONResponse); !ok {
		t.Fatalf("viewer create folder: %T", r)
	}
}

func TestSpaces_AccessListAndForceDelete(t *testing.T) {
	t.Parallel()
	f := newMetaFixture(t)
	ws := sharedWorkspace(t, f)
	owner := f.as(f.owner)
	space := createSpaceIn(t, f, owner, ws, CreateSpaceRequest{
		Name: "Shared", Visibility: CreateSpaceRequestVisibilityWorkspace,
	})

	set, err := f.h.SetSpaceMember(owner, SetSpaceMemberRequestObject{
		Id: ws, SpaceId: space.Id, PrincipalType: "user", PrincipalId: oid(t, f.stranger),
		Body: &SetSpaceMemberJSONRequestBody{Role: SetSpaceMemberRequestRoleEditor},
	})
	if err != nil {
		t.Fatal(err)
	}
	if m := set.(SetSpaceMember200JSONResponse); m.Role != SpaceMemberRoleEditor {
		t.Fatalf("member = %+v", m)
	}
	members, err := f.h.ListSpaceMembers(owner, ListSpaceMembersRequestObject{Id: ws, SpaceId: space.Id})
	if err != nil {
		t.Fatal(err)
	}
	got := members.(ListSpaceMembers200JSONResponse)
	if len(got) != 2 { // the creator (manager) and the new editor
		t.Fatalf("members = %+v", got)
	}
	found := false
	for _, m := range got {
		if m.PrincipalId == oid(t, f.stranger) && m.Role == SpaceMemberRoleEditor {
			found = true
		}
	}
	if !found {
		t.Fatalf("member entry missing: %+v", got)
	}

	if r, err := f.h.CreateFolder(owner, CreateFolderRequestObject{
		Id: ws, Body: &CreateFolderRequest{Name: "keep", SpaceId: &space.Id},
	}); err != nil {
		t.Fatal(err)
	} else if _, ok := r.(CreateFolder201JSONResponse); !ok {
		t.Fatalf("create folder: %T", r)
	}
	if r, err := f.h.DeleteSpace(owner, DeleteSpaceRequestObject{Id: ws, SpaceId: space.Id}); err != nil {
		t.Fatal(err)
	} else if _, ok := r.(DeleteSpace409JSONResponse); !ok {
		t.Fatalf("delete non-empty space: %T", r)
	}
	force := true
	if r, err := f.h.DeleteSpace(owner, DeleteSpaceRequestObject{
		Id: ws, SpaceId: space.Id, Params: DeleteSpaceParams{Force: &force},
	}); err != nil {
		t.Fatal(err)
	} else if _, ok := r.(DeleteSpace204Response); !ok {
		t.Fatalf("force delete: %T", r)
	}
	var n int64
	if err := f.gdb.Model(&model.QueryFolder{}).
		Where("space_id = ?", repo.UUIDString(pgUUIDOf(space.Id))).Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("folders after force delete = %d", n)
	}

	if r, err := f.h.DeleteSpaceMember(owner, DeleteSpaceMemberRequestObject{
		Id: ws, SpaceId: space.Id, PrincipalType: "user", PrincipalId: oid(t, f.stranger),
	}); err != nil {
		t.Fatal(err)
	} else if _, ok := r.(DeleteSpaceMember404JSONResponse); !ok {
		t.Fatalf("delete removed member: %T", r)
	}
}

// TestSpaces_CreateRequiresWorkspaceOwner pins #612: a member who is not a
// workspace owner cannot create a space (which would make them its manager).
func TestSpaces_CreateRequiresWorkspaceOwner(t *testing.T) {
	t.Parallel()
	f := newMetaFixture(t)
	ws := sharedWorkspace(t, f)
	member := f.as(f.stranger)

	r, err := f.h.CreateSpace(member, CreateSpaceRequestObject{
		Id: ws, Body: &CreateSpaceRequest{Name: "escalate", Visibility: CreateSpaceRequestVisibilityWorkspace},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := r.(CreateSpace403JSONResponse); !ok {
		t.Fatalf("non-owner create: %T, want 403", r)
	}

	// The workspace owner still creates.
	createSpaceIn(t, f, f.as(f.owner), ws, CreateSpaceRequest{Name: "ok", Visibility: CreateSpaceRequestVisibilityWorkspace})
}

func TestSpaces_ForeignWorkspace404(t *testing.T) {
	t.Parallel()
	f := newMetaFixture(t)
	ctx := f.as(f.stranger)
	r, err := f.h.ListSpaces(ctx, ListSpacesRequestObject{Id: f.ws})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := r.(ListSpaces404JSONResponse); !ok {
		t.Fatalf("foreign list: %T", r)
	}
	c, err := f.h.CreateSpace(ctx, CreateSpaceRequestObject{
		Id: f.ws, Body: &CreateSpaceRequest{Name: "x", Visibility: CreateSpaceRequestVisibilityWorkspace},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := c.(CreateSpace404JSONResponse); !ok {
		t.Fatalf("foreign create: %T", c)
	}
}

// brokenPrincipals fails every user and team lookup.
type brokenPrincipals struct{ db.Querier }

func (brokenPrincipals) GetUserByID(context.Context, pgtype.UUID) (db.User, error) {
	return db.User{}, errors.New("connection reset")
}

func (brokenPrincipals) GetTeamByID(context.Context, pgtype.UUID) (db.Team, error) {
	return db.Team{}, errors.New("connection reset")
}

// TestSetSpaceMember_LookupFailureIsAnError: a failed principal lookup is a
// server error, not "user not found".
func TestSetSpaceMember_LookupFailureIsAnError(t *testing.T) {
	t.Parallel()
	f := newMetaFixture(t)
	ws := sharedWorkspace(t, f)
	owner := f.as(f.owner)
	space := createSpaceIn(t, f, owner, ws, CreateSpaceRequest{Name: "Shared", Visibility: CreateSpaceRequestVisibilityWorkspace})
	broken := brokenPrincipals{repo.New(testdb.GORM(t, f.pool))}
	f.h.queries = broken
	f.h.store = nil // exercise the sqlc path this fixture deliberately breaks
	f.h.access = wsaccess.New(f.h.queries)
	f.h.topology = topology.New(f.h.queries)
	f.h.spaces = spacesOver(f.h.core, broken)
	for _, kind := range []string{"user", "team"} {
		_, err := f.h.SetSpaceMember(owner, SetSpaceMemberRequestObject{
			Id: ws, SpaceId: space.Id, PrincipalType: kind, PrincipalId: oid(t, f.stranger),
			Body: &SetSpaceMemberJSONRequestBody{Role: SetSpaceMemberRequestRoleViewer},
		})
		if err == nil {
			t.Errorf("%s lookup failure was answered with a response", kind)
		}
	}
}
