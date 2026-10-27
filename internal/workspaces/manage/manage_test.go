package manage

import (
	"context"
	"errors"
	"regexp"
	"testing"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func newID() pgtype.UUID { return pgtype.UUID{Bytes: uuid.New(), Valid: true} }

type fakeStore struct {
	workspaces map[[16]byte]db.Workspace
	grants     map[[2][16]byte]string
	running    int64
	defaults   []db.SetWorkspaceDefaultTargetParams
	// clearErr fails clearing a default, to check Revoke is atomic.
	clearErr error
}

func newStore() *fakeStore {
	return &fakeStore{workspaces: map[[16]byte]db.Workspace{}, grants: map[[2][16]byte]string{}}
}

func (f *fakeStore) ListWorkspacesByUser(context.Context, pgtype.UUID) ([]db.Workspace, error) {
	return nil, nil
}

func (f *fakeStore) GetWorkspaceByID(_ context.Context, id pgtype.UUID) (db.Workspace, error) {
	if w, ok := f.workspaces[id.Bytes]; ok {
		return w, nil
	}
	return db.Workspace{}, pgx.ErrNoRows
}

func (f *fakeStore) ListWorkspaceCatalogGrants(context.Context, pgtype.UUID) ([]db.ListWorkspaceCatalogGrantsRow, error) {
	return nil, nil
}

func (f *fakeStore) CountWorkspaceRunningExecutions(context.Context, pgtype.UUID) (int64, error) {
	return f.running, nil
}

func (f *fakeStore) CountWorkspaceImpact(context.Context, pgtype.UUID) (db.CountWorkspaceImpactRow, error) {
	return db.CountWorkspaceImpactRow{}, nil
}

func (f *fakeStore) CountCatalogAccessImpact(context.Context, db.CountCatalogAccessImpactParams) (db.CountCatalogAccessImpactRow, error) {
	return db.CountCatalogAccessImpactRow{}, nil
}

func (f *fakeStore) CreateWorkspace(_ context.Context, arg db.CreateWorkspaceParams) (db.Workspace, error) {
	w := db.Workspace{ID: newID(), Name: arg.Name, Type: arg.Type, PolarisCatalogName: arg.PolarisCatalogName}
	f.workspaces[w.ID.Bytes] = w
	return w, nil
}

func (f *fakeStore) DeleteWorkspace(_ context.Context, id pgtype.UUID) (int64, error) {
	if _, ok := f.workspaces[id.Bytes]; !ok {
		return 0, nil
	}
	delete(f.workspaces, id.Bytes)
	return 1, nil
}

func (f *fakeStore) UpsertWorkspaceCatalogGrant(_ context.Context, arg db.UpsertWorkspaceCatalogGrantParams) (db.WorkspaceCatalogGrant, error) {
	f.grants[[2][16]byte{arg.WorkspaceID.Bytes, arg.CatalogID.Bytes}] = arg.Permission
	return db.WorkspaceCatalogGrant{WorkspaceID: arg.WorkspaceID, CatalogID: arg.CatalogID, Permission: arg.Permission}, nil
}

func (f *fakeStore) UpdateWorkspace(_ context.Context, arg db.UpdateWorkspaceParams) (db.Workspace, error) {
	w := f.workspaces[arg.ID.Bytes]
	w.Name, w.Description = arg.Name, arg.Description
	f.workspaces[arg.ID.Bytes] = w
	return w, nil
}

func (f *fakeStore) SetWorkspaceDefaultTarget(_ context.Context, arg db.SetWorkspaceDefaultTargetParams) error {
	f.defaults = append(f.defaults, arg)
	return nil
}

func (f *fakeStore) DeleteWorkspaceCatalogGrant(_ context.Context, arg db.DeleteWorkspaceCatalogGrantParams) (int64, error) {
	k := [2][16]byte{arg.WorkspaceID.Bytes, arg.CatalogID.Bytes}
	if _, ok := f.grants[k]; !ok {
		return 0, nil
	}
	delete(f.grants, k)
	return 1, nil
}

func (f *fakeStore) ClearWorkspaceDefaultForCatalog(context.Context, db.ClearWorkspaceDefaultForCatalogParams) error {
	return f.clearErr
}

// tx restores the grants when fn fails.
func (f *fakeStore) tx(_ context.Context, fn func(TxStore) error) error {
	saved := map[[2][16]byte]string{}
	for k, v := range f.grants {
		saved[k] = v
	}
	if err := fn(f); err != nil {
		f.grants = saved
		return err
	}
	return nil
}

func TestCreateAndUpdate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStore()
	s := New(st, st.tx)
	w, err := s.Create(ctx, NewWorkspace{Name: "My Workspace!", Type: "shared", OwnerTeam: newID()})
	if err != nil || !regexp.MustCompile(`^my_workspace__\d+$`).MatchString(w.PolarisCatalogName) {
		t.Fatalf("Create = %+v, %v; want a derived Polaris name", w, err)
	}

	name := "Renamed"
	got, err := s.Update(ctx, w, Edit{Name: &name})
	if err != nil || got.Name != name || len(st.defaults) != 0 {
		t.Fatalf("rename = %+v, %v; want no default change", got, err)
	}
	binding := newID()
	got, err = s.Update(ctx, w, Edit{Default: &Default{Binding: binding}})
	if err != nil || got.DefaultBindingID != binding || len(st.defaults) != 1 {
		t.Errorf("set default = %+v, %v", got, err)
	}
	if got, _ = s.Update(ctx, w, Edit{Default: &Default{}}); got.DefaultBindingID.Valid {
		t.Error("an empty Default clears the target")
	}
}

func TestDelete(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStore()
	s := New(st, st.tx)
	if err := s.Delete(ctx, db.Workspace{Type: "personal"}); !errors.Is(err, ErrPersonal) {
		t.Errorf("personal = %v, want ErrPersonal", err)
	}
	w, _ := s.Create(ctx, NewWorkspace{Name: "x", Type: "shared"})
	st.running = 1
	if err := s.Delete(ctx, w); !errors.Is(err, ErrExecutionsRunning) {
		t.Errorf("running = %v, want ErrExecutionsRunning", err)
	}
	st.running = 0
	if err := s.Delete(ctx, w); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, w); !errors.Is(err, ErrNotFound) {
		t.Errorf("second delete = %v, want ErrNotFound", err)
	}
}

func TestGrantAndRevoke(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStore()
	s := New(st, st.tx)
	ws, cat := newID(), newID()
	if _, err := s.Grant(ctx, ws, cat, "admin", newID()); !errors.Is(err, ErrInvalidPermission) {
		t.Errorf("bad permission = %v", err)
	}
	if _, err := s.Grant(ctx, ws, cat, "read", newID()); err != nil {
		t.Fatal(err)
	}

	st.clearErr = errors.New("connection reset")
	if err := s.Revoke(ctx, ws, cat); !errors.Is(err, st.clearErr) {
		t.Fatalf("Revoke = %v, want the clear error", err)
	}
	if len(st.grants) != 1 {
		t.Error("a failed revoke must keep the grant")
	}
	st.clearErr = nil
	if err := s.Revoke(ctx, ws, cat); err != nil {
		t.Fatal(err)
	}
	if err := s.Revoke(ctx, ws, cat); !errors.Is(err, ErrGrantNotFound) {
		t.Errorf("second revoke = %v, want ErrGrantNotFound", err)
	}
}
