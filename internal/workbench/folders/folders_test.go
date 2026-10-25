package folders

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

type fakeStore struct {
	folders map[[16]byte]db.QueryFolder
	// createErr makes the insert fail, as a concurrent create of the same
	// slug would.
	createErr error
}

func newStore() *fakeStore { return &fakeStore{folders: map[[16]byte]db.QueryFolder{}} }

func newID() pgtype.UUID { return pgtype.UUID{Bytes: uuid.New(), Valid: true} }

func (f *fakeStore) ListVisibleQueryFolders(_ context.Context, arg db.ListVisibleQueryFoldersParams) ([]db.QueryFolder, error) {
	var out []db.QueryFolder
	for _, q := range f.folders {
		if q.WorkspaceID == arg.WorkspaceID && q.ParentID == arg.ParentID {
			out = append(out, q)
		}
	}
	return out, nil
}

func (f *fakeStore) ListAllVisibleQueryFolders(_ context.Context, arg db.ListAllVisibleQueryFoldersParams) ([]db.QueryFolder, error) {
	var out []db.QueryFolder
	for _, q := range f.folders {
		if q.WorkspaceID == arg.WorkspaceID {
			out = append(out, q)
		}
	}
	return out, nil
}

func (f *fakeStore) GetQueryFolderByID(_ context.Context, arg db.GetQueryFolderByIDParams) (db.QueryFolder, error) {
	if q, ok := f.folders[arg.ID.Bytes]; ok && q.WorkspaceID == arg.WorkspaceID {
		return q, nil
	}
	return db.QueryFolder{}, pgx.ErrNoRows
}

func (f *fakeStore) FolderSlugExists(_ context.Context, arg db.FolderSlugExistsParams) (bool, error) {
	for _, q := range f.folders {
		if q.SpaceID == arg.SpaceID && q.Slug == arg.Slug {
			return true, nil
		}
	}
	return false, nil
}

func (f *fakeStore) CreateQueryFolder(_ context.Context, arg db.CreateQueryFolderParams) (db.QueryFolder, error) {
	if f.createErr != nil {
		return db.QueryFolder{}, f.createErr
	}
	q := db.QueryFolder{ID: newID(), WorkspaceID: arg.WorkspaceID, SpaceID: arg.SpaceID, ParentID: arg.ParentID, Name: arg.Name, Slug: arg.Slug}
	f.folders[q.ID.Bytes] = q
	return q, nil
}

func (f *fakeStore) DeleteQueryFolder(_ context.Context, arg db.DeleteQueryFolderParams) (int64, error) {
	if q, ok := f.folders[arg.ID.Bytes]; ok && q.WorkspaceID == arg.WorkspaceID {
		delete(f.folders, arg.ID.Bytes)
		return 1, nil
	}
	return 0, nil
}

func TestCreate(t *testing.T) {
	ctx := context.Background()
	st := newStore()
	s := New(st)
	ws, space, otherSpace := newID(), newID(), newID()

	a, err := s.Create(ctx, ws, space, nil, "Monthly Reports", "")
	if err != nil || a.Slug != "monthly_reports" {
		t.Fatalf("Create = %+v, %v", a, err)
	}
	b, _ := s.Create(ctx, ws, space, nil, "Monthly reports", "")
	if b.Slug != "monthly_reports_2" {
		t.Errorf("second slug = %q, want it made unique in the space", b.Slug)
	}
	if c, _ := s.Create(ctx, ws, otherSpace, nil, "Monthly reports", ""); c.Slug != "monthly_reports" {
		t.Errorf("slug in another space = %q; uniqueness is per space", c.Slug)
	}

	child, err := s.Create(ctx, ws, space, &a.ID, "Q1", "q1")
	if err != nil || child.ParentID != a.ID {
		t.Fatalf("child = %+v, %v", child, err)
	}
	if _, err := s.Create(ctx, ws, otherSpace, &a.ID, "x", ""); !errors.Is(err, ErrParentNotFound) {
		t.Errorf("parent in another space = %v, want ErrParentNotFound", err)
	}
	missing := newID()
	if _, err := s.Create(ctx, ws, space, &missing, "x", ""); !errors.Is(err, ErrParentNotFound) {
		t.Errorf("missing parent = %v, want ErrParentNotFound", err)
	}
	if _, err := s.Create(ctx, ws, space, nil, "x", "Not A Slug"); !errors.Is(err, ErrInvalidSlug) {
		t.Errorf("bad slug = %v, want ErrInvalidSlug", err)
	}
	st.createErr = &pgconn.PgError{Code: "23505"}
	if _, err := s.Create(ctx, ws, space, nil, "x", "taken"); !errors.Is(err, ErrSlugTaken) {
		t.Errorf("duplicate = %v, want ErrSlugTaken", err)
	}
}

func TestListGetDelete(t *testing.T) {
	ctx := context.Background()
	st := newStore()
	s := New(st)
	ws, space := newID(), newID()
	root, _ := s.Create(ctx, ws, space, nil, "root", "")
	_, _ = s.Create(ctx, ws, space, &root.ID, "child", "")

	if all, _ := s.List(ctx, ws, newID(), pgtype.UUID{}, nil); len(all) != 2 {
		t.Errorf("all = %d, want 2", len(all))
	}
	if kids, _ := s.List(ctx, ws, newID(), pgtype.UUID{}, &root.ID); len(kids) != 1 {
		t.Errorf("children = %d, want 1", len(kids))
	}
	if _, err := s.Get(ctx, newID(), root.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("folder of another workspace = %v, want ErrNotFound", err)
	}
	if err := s.Delete(ctx, ws, root.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, ws, root.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("second delete = %v, want ErrNotFound", err)
	}
}
