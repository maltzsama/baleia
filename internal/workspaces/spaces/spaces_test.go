package spaces

import (
	"context"
	"errors"
	"testing"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

func newID() pgtype.UUID { return pgtype.UUID{Bytes: uuid.New(), Valid: true} }

func ptr(s string) *string { return &s }

// fakeStore keeps spaces and members; names are unique per store.
type fakeStore struct {
	spaces  map[[16]byte]db.Space
	members map[[16]byte][]db.SpaceMember
	content int32
	users   map[[16]byte]bool
	// failMember fails SetSpaceMember, to check Create is atomic.
	failMember error
}

func newStore() *fakeStore {
	return &fakeStore{spaces: map[[16]byte]db.Space{}, members: map[[16]byte][]db.SpaceMember{}, users: map[[16]byte]bool{}}
}

func (f *fakeStore) ListSpacesForPrincipal(context.Context, db.ListSpacesForPrincipalParams) ([]db.ListSpacesForPrincipalRow, error) {
	return nil, nil
}

func (f *fakeStore) GetSpace(_ context.Context, arg db.GetSpaceParams) (db.Space, error) {
	if sp, ok := f.spaces[arg.ID.Bytes]; ok && sp.WorkspaceID == arg.WorkspaceID {
		return sp, nil
	}
	return db.Space{}, pgx.ErrNoRows
}

func (f *fakeStore) GetDefaultSpace(context.Context, pgtype.UUID) (db.Space, error) {
	return db.Space{}, pgx.ErrNoRows
}

func (f *fakeStore) SpaceSlugExists(_ context.Context, arg db.SpaceSlugExistsParams) (bool, error) {
	for _, sp := range f.spaces {
		if sp.Slug == arg.Slug {
			return true, nil
		}
	}
	return false, nil
}

func (f *fakeStore) CountSpaceContent(context.Context, pgtype.UUID) (int32, error) {
	return f.content, nil
}

func (f *fakeStore) CountSpaceContentKinds(context.Context, pgtype.UUID) (db.CountSpaceContentKindsRow, error) {
	return db.CountSpaceContentKindsRow{}, nil
}

func (f *fakeStore) ListSpaceMembers(_ context.Context, id pgtype.UUID) ([]db.SpaceMember, error) {
	return f.members[id.Bytes], nil
}

func (f *fakeStore) GetUserByID(_ context.Context, id pgtype.UUID) (db.User, error) {
	if !f.users[id.Bytes] {
		return db.User{}, pgx.ErrNoRows
	}
	return db.User{ID: id}, nil
}

func (f *fakeStore) GetTeamByID(context.Context, pgtype.UUID) (db.Team, error) {
	return db.Team{}, errors.New("connection reset")
}

func (f *fakeStore) nameTaken(name string, except pgtype.UUID) bool {
	for _, sp := range f.spaces {
		if sp.Name == name && sp.ID != except {
			return true
		}
	}
	return false
}

func (f *fakeStore) CreateSpace(_ context.Context, arg db.CreateSpaceParams) (db.Space, error) {
	if f.nameTaken(arg.Name, pgtype.UUID{}) {
		return db.Space{}, &pgconn.PgError{Code: "23505"}
	}
	sp := db.Space{ID: newID(), WorkspaceID: arg.WorkspaceID, Name: arg.Name, Slug: arg.Slug, Visibility: arg.Visibility, DefaultRole: arg.DefaultRole}
	f.spaces[sp.ID.Bytes] = sp
	return sp, nil
}

func (f *fakeStore) UpdateSpace(_ context.Context, arg db.UpdateSpaceParams) (db.Space, error) {
	if f.nameTaken(arg.Name, arg.ID) {
		return db.Space{}, &pgconn.PgError{Code: "23505"}
	}
	sp := f.spaces[arg.ID.Bytes]
	sp.Name, sp.Visibility, sp.DefaultRole = arg.Name, arg.Visibility, arg.DefaultRole
	f.spaces[arg.ID.Bytes] = sp
	return sp, nil
}

func (f *fakeStore) DeleteSpace(_ context.Context, arg db.DeleteSpaceParams) (int64, error) {
	if _, ok := f.spaces[arg.ID.Bytes]; !ok {
		return 0, nil
	}
	delete(f.spaces, arg.ID.Bytes)
	return 1, nil
}

func (f *fakeStore) SetSpaceMember(_ context.Context, arg db.SetSpaceMemberParams) (db.SpaceMember, error) {
	if f.failMember != nil {
		return db.SpaceMember{}, f.failMember
	}
	m := db.SpaceMember{SpaceID: arg.SpaceID, PrincipalType: arg.PrincipalType, PrincipalID: arg.PrincipalID, Role: arg.Role}
	f.members[arg.SpaceID.Bytes] = append(f.members[arg.SpaceID.Bytes], m)
	return m, nil
}

func (f *fakeStore) DeleteSpaceMember(_ context.Context, arg db.DeleteSpaceMemberParams) (int64, error) {
	list := f.members[arg.SpaceID.Bytes]
	for i, m := range list {
		if m.PrincipalID == arg.PrincipalID && m.PrincipalType == arg.PrincipalType {
			f.members[arg.SpaceID.Bytes] = append(list[:i], list[i+1:]...)
			return 1, nil
		}
	}
	return 0, nil
}

// tx rolls the spaces back when fn fails.
func (f *fakeStore) tx(_ context.Context, fn func(TxStore) error) error {
	saved := map[[16]byte]db.Space{}
	for k, v := range f.spaces {
		saved[k] = v
	}
	if err := fn(f); err != nil {
		f.spaces = saved
		return err
	}
	return nil
}

func TestCreate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStore()
	s := New(st, st.tx)
	ws, actor := newID(), newID()

	var inv *InvalidError
	for name, f := range map[string]Fields{
		"blank name":   {Name: ptr(" "), Visibility: ptr("workspace")},
		"bad vis":      {Name: ptr("x"), Visibility: ptr("public")},
		"bad def role": {Name: ptr("x"), Visibility: ptr("workspace"), DefaultRole: ptr("manager")},
	} {
		if _, err := s.Create(ctx, ws, actor, f); !errors.As(err, &inv) {
			t.Errorf("%s = %v, want an InvalidError", name, err)
		}
	}
	sp, err := s.Create(ctx, ws, actor, Fields{Name: ptr(" Finance "), Visibility: ptr("restricted")})
	if err != nil || sp.Name != "Finance" || sp.DefaultRole != "viewer" {
		t.Fatalf("Create = %+v, %v; want the trimmed name and viewer default", sp, err)
	}
	if m := st.members[sp.ID.Bytes]; len(m) != 1 || m[0].Role != "manager" || m[0].PrincipalID != actor {
		t.Errorf("members = %+v; want the creator as manager", m)
	}
	if _, err := s.Create(ctx, ws, actor, Fields{Name: ptr("Finance"), Visibility: ptr("workspace")}); !errors.Is(err, ErrNameTaken) {
		t.Errorf("same name = %v, want ErrNameTaken", err)
	}

	st.failMember = errors.New("connection reset")
	if _, err := s.Create(ctx, ws, actor, Fields{Name: ptr("Ops"), Visibility: ptr("workspace")}); !errors.Is(err, st.failMember) {
		t.Fatalf("Create = %v, want the member error", err)
	}
	if len(st.spaces) != 1 {
		t.Error("a space without its manager was left behind")
	}
}

func TestUpdateAndDelete(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStore()
	s := New(st, st.tx)
	ws := newID()
	def := db.Space{ID: newID(), WorkspaceID: ws, Name: "General", Visibility: "workspace", DefaultRole: "viewer", IsDefault: true}
	st.spaces[def.ID.Bytes] = def
	other, _ := s.Create(ctx, ws, newID(), Fields{Name: ptr("Ops"), Visibility: ptr("workspace")})

	var inv *InvalidError
	if _, err := s.Update(ctx, ws, def.ID, Fields{Visibility: ptr("restricted")}); !errors.As(err, &inv) {
		t.Errorf("restricting the default space = %v, want an InvalidError", err)
	}
	if _, err := s.Update(ctx, ws, other.ID, Fields{Name: ptr("General")}); !errors.Is(err, ErrNameTaken) {
		t.Errorf("taken name = %v, want ErrNameTaken", err)
	}

	if err := s.Delete(ctx, ws, def.ID, true); !errors.Is(err, ErrDefaultSpace) {
		t.Errorf("delete default = %v, want ErrDefaultSpace", err)
	}
	st.content = 2
	if err := s.Delete(ctx, ws, other.ID, false); !errors.Is(err, ErrNotEmpty) {
		t.Errorf("delete with content = %v, want ErrNotEmpty", err)
	}
	if err := s.Delete(ctx, ws, other.ID, true); err != nil {
		t.Errorf("forced delete = %v", err)
	}
}

func TestMembers(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStore()
	s := New(st, st.tx)
	space, user := newID(), newID()
	st.users[user.Bytes] = true

	var inv *InvalidError
	if _, err := s.SetMember(ctx, space, "group", user, "viewer", newID()); !errors.As(err, &inv) {
		t.Errorf("bad principal type = %v", err)
	}
	if _, err := s.SetMember(ctx, space, "user", newID(), "viewer", newID()); !errors.Is(err, ErrUserNotFound) {
		t.Errorf("unknown user = %v", err)
	}
	if _, err := s.SetMember(ctx, space, "team", newID(), "viewer", newID()); err == nil || errors.Is(err, ErrTeamNotFound) {
		t.Errorf("team lookup failure = %v, want the store error", err)
	}
	if _, err := s.SetMember(ctx, space, "user", user, "owner", newID()); !errors.As(err, &inv) {
		t.Errorf("bad role = %v", err)
	}
	if _, err := s.SetMember(ctx, space, "user", user, "editor", newID()); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveMember(ctx, space, "user", user); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveMember(ctx, space, "user", user); !errors.Is(err, ErrMemberNotFound) {
		t.Errorf("second remove = %v, want ErrMemberNotFound", err)
	}
}
