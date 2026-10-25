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
	// moved and movedQueries record the subtree moves.
	moved, movedQueries []pgtype.UUID
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

// brokenStore fails folder reads the way a lost connection does.
type brokenStore struct{ *fakeStore }

var errDown = errors.New("connection refused")

func (brokenStore) GetQueryFolderByID(context.Context, db.GetQueryFolderByIDParams) (db.QueryFolder, error) {
	return db.QueryFolder{}, errDown
}

// TestStoreFailureIsNotNotFound: a database outage is an error, never a
// missing folder or parent.
func TestStoreFailureIsNotNotFound(t *testing.T) {
	s := New(brokenStore{newStore()})
	if _, err := s.Get(context.Background(), newID(), newID()); !errors.Is(err, errDown) {
		t.Errorf("Get = %v, want the store error", err)
	}
	parent := newID()
	if _, err := s.Create(context.Background(), newID(), newID(), &parent, "x", ""); !errors.Is(err, errDown) || errors.Is(err, ErrParentNotFound) {
		t.Errorf("Create = %v, want the store error", err)
	}
}

func (f *fakeStore) UpdateQueryFolder(_ context.Context, arg db.UpdateQueryFolderParams) (db.QueryFolder, error) {
	q := f.folders[arg.ID.Bytes]
	q.Name, q.Slug, q.ParentID, q.SpaceID = arg.Name, *arg.Slug, arg.ParentID, arg.SpaceID
	f.folders[arg.ID.Bytes] = q
	return q, nil
}

func (f *fakeStore) MoveQueryFolderSubtree(_ context.Context, arg db.MoveQueryFolderSubtreeParams) error {
	f.moved = append(f.moved, arg.ID)
	return nil
}

func (f *fakeStore) MoveSavedQueriesInFolderSubtree(_ context.Context, arg db.MoveSavedQueriesInFolderSubtreeParams) error {
	f.movedQueries = append(f.movedQueries, arg.ID)
	return nil
}

func TestUpdate(t *testing.T) {
	ctx := context.Background()
	st := newStore()
	s := New(st)
	ws, space, other := newID(), newID(), newID()
	root, _ := s.Create(ctx, ws, space, nil, "root", "")
	child, _ := s.Create(ctx, ws, space, &root.ID, "child", "")

	blank, bad := "  ", "Bad Slug"
	if _, err := s.Update(ctx, root, Edit{Name: &blank, SpaceID: space}); !errors.Is(err, ErrEmptyName) {
		t.Errorf("blank name = %v", err)
	}
	if _, err := s.Update(ctx, root, Edit{Slug: &bad, SpaceID: space}); !errors.Is(err, ErrInvalidSlug) {
		t.Errorf("bad slug = %v", err)
	}
	var cycle *CycleError
	if _, err := s.Update(ctx, root, Edit{SpaceID: space, Parent: &root.ID}); !errors.As(err, &cycle) {
		t.Errorf("own parent = %v", err)
	}
	if _, err := s.Update(ctx, root, Edit{SpaceID: space, Parent: &child.ID}); !errors.As(err, &cycle) {
		t.Errorf("under a descendant = %v", err)
	}

	// Moving to another space without a parent lands at its root and takes
	// the subtree along.
	moved, err := s.Update(ctx, child, Edit{SpaceID: other})
	if err != nil || moved.SpaceID != other || moved.ParentID.Valid {
		t.Fatalf("move = %+v, %v", moved, err)
	}
	if len(st.moved) != 1 || len(st.movedQueries) != 1 {
		t.Errorf("subtree moves = %d folders, %d queries; want both", len(st.moved), len(st.movedQueries))
	}

	// A slug already used in the destination space is refused.
	_, _ = s.Create(ctx, ws, other, nil, "root", "")
	if _, err := s.Update(ctx, root, Edit{SpaceID: other}); !errors.Is(err, ErrSlugConflict) {
		t.Errorf("slug taken in the destination = %v", err)
	}
}
