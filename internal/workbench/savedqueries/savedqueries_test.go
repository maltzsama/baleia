package savedqueries

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

// fakeStore is the read side and, as fakeContent, the versioned write side.
type fakeStore struct {
	queries  map[[16]byte]db.SavedQuery
	versions map[[16]byte]int32
	// nameTaken makes the next write fail on the unique name.
	nameTaken bool
}

func newStore() *fakeStore {
	return &fakeStore{queries: map[[16]byte]db.SavedQuery{}, versions: map[[16]byte]int32{}}
}

func (f *fakeStore) ListVisibleSavedQueries(_ context.Context, arg db.ListVisibleSavedQueriesParams) ([]db.SavedQuery, error) {
	var out []db.SavedQuery
	for _, q := range f.queries {
		if q.WorkspaceID == arg.WorkspaceID && q.FolderID == arg.FolderID {
			out = append(out, q)
		}
	}
	return out, nil
}

func (f *fakeStore) SearchVisibleSavedQueries(_ context.Context, arg db.SearchVisibleSavedQueriesParams) ([]db.SavedQuery, error) {
	var out []db.SavedQuery
	for _, q := range f.queries {
		if q.WorkspaceID == arg.WorkspaceID {
			out = append(out, q)
		}
	}
	return out, nil
}

func (f *fakeStore) ListSavedQueryVersions(context.Context, pgtype.UUID) ([]db.ListSavedQueryVersionsRow, error) {
	var out []db.ListSavedQueryVersionsRow
	for id, v := range f.versions {
		out = append(out, db.ListSavedQueryVersionsRow{EntityID: pgtype.UUID{Bytes: id, Valid: true}, Version: v})
	}
	return out, nil
}

func (f *fakeStore) GetSavedQueryByID(_ context.Context, arg db.GetSavedQueryByIDParams) (db.SavedQuery, error) {
	if q, ok := f.queries[arg.ID.Bytes]; ok && q.WorkspaceID == arg.WorkspaceID {
		return q, nil
	}
	return db.SavedQuery{}, pgx.ErrNoRows
}

func (f *fakeStore) SavedQuerySlugExists(_ context.Context, arg db.SavedQuerySlugExistsParams) (bool, error) {
	for _, q := range f.queries {
		if q.WorkspaceID == arg.WorkspaceID && q.Slug == arg.Slug {
			return true, nil
		}
	}
	return false, nil
}

func (f *fakeStore) CreateSavedQuery(_ context.Context, _ pgtype.UUID, in db.CreateSavedQueryParams) (db.SavedQuery, error) {
	if f.nameTaken {
		return db.SavedQuery{}, &pgconn.PgError{Code: "23505"}
	}
	q := db.SavedQuery{ID: newID(), WorkspaceID: in.WorkspaceID, SpaceID: in.SpaceID, FolderID: in.FolderID, Name: in.Name, Slug: in.Slug, Sql: in.Sql, TargetBindingID: in.TargetBindingID, TargetSchema: in.TargetSchema}
	f.queries[q.ID.Bytes] = q
	f.versions[q.ID.Bytes] = 1
	return q, nil
}

func (f *fakeStore) UpdateSavedQuery(_ context.Context, _ pgtype.UUID, in db.UpdateSavedQueryParams, _ *int32) (db.SavedQuery, bool, error) {
	if f.nameTaken {
		return db.SavedQuery{}, false, &pgconn.PgError{Code: "23505"}
	}
	q := f.queries[in.ID.Bytes]
	q.Name, q.Sql, q.FolderID, q.SpaceID, q.Slug, q.TargetBindingID, q.TargetSchema = in.Name, in.Sql, in.FolderID, in.SpaceID, in.Slug, in.TargetBindingID, in.TargetSchema
	f.queries[in.ID.Bytes] = q
	f.versions[in.ID.Bytes]++
	return q, true, nil
}

func (f *fakeStore) DeleteSavedQuery(_ context.Context, _ pgtype.UUID, _, id pgtype.UUID) error {
	delete(f.queries, id.Bytes)
	return nil
}

// fakeFolders knows one folder per space.
type fakeFolders map[[16]byte]db.QueryFolder

func (f fakeFolders) Get(_ context.Context, ws, id pgtype.UUID) (db.QueryFolder, error) {
	if q, ok := f[id.Bytes]; ok && q.WorkspaceID == ws {
		return q, nil
	}
	return db.QueryFolder{}, errors.New("folder not found")
}

type fixture struct {
	s             *Service
	st            *fakeStore
	ws, space     pgtype.UUID
	folder, other db.QueryFolder
}

func setup() fixture {
	ws, space := newID(), newID()
	folder := db.QueryFolder{ID: newID(), WorkspaceID: ws, SpaceID: space}
	other := db.QueryFolder{ID: newID(), WorkspaceID: ws, SpaceID: newID()}
	st := newStore()
	f := fakeFolders{folder.ID.Bytes: folder, other.ID.Bytes: other}
	return fixture{s: NewService(st, f, st), st: st, ws: ws, space: space, folder: folder, other: other}
}

func TestCreate(t *testing.T) {
	ctx := context.Background()
	f := setup()
	binding := newID()
	target := func(context.Context) (pgtype.UUID, *string, error) { return binding, nil, nil }

	q, err := f.s.Create(ctx, newID(), f.ws, New{SpaceID: f.space, FolderID: f.folder.ID, Name: "Revenue by month", SQL: "select 1", Target: target})
	if err != nil || q.Slug != "revenue_by_month" || q.TargetBindingID != binding || q.FolderID != f.folder.ID {
		t.Fatalf("Create = %+v, %v", q, err)
	}
	if q2, _ := f.s.Create(ctx, newID(), f.ws, New{SpaceID: f.space, Name: "Revenue by month!", SQL: "select 2"}); q2.Slug != "revenue_by_month_2" {
		t.Errorf("second slug = %q, want it made unique", q2.Slug)
	}

	for _, c := range []struct {
		name string
		in   New
		want error
	}{
		{"folder of another space", New{SpaceID: f.space, FolderID: f.other.ID, Name: "x"}, ErrFolderNotFound},
		{"unknown folder", New{SpaceID: f.space, FolderID: newID(), Name: "x"}, ErrFolderNotFound},
		{"bad slug", New{SpaceID: f.space, Name: "x", Slug: "Bad Slug"}, ErrInvalidSlug},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, err := f.s.Create(ctx, newID(), f.ws, c.in); !errors.Is(err, c.want) {
				t.Errorf("err = %v, want %v", err, c.want)
			}
		})
	}

	// The target is resolved last: a folder error wins over a target error.
	badTarget := errors.New("target not granted")
	failing := func(context.Context) (pgtype.UUID, *string, error) { return pgtype.UUID{}, nil, badTarget }
	if _, err := f.s.Create(ctx, newID(), f.ws, New{SpaceID: f.space, FolderID: newID(), Name: "x", Target: failing}); !errors.Is(err, ErrFolderNotFound) {
		t.Errorf("folder and target errors = %v, want the folder error first", err)
	}
	if _, err := f.s.Create(ctx, newID(), f.ws, New{SpaceID: f.space, Name: "x", Target: failing}); !errors.Is(err, badTarget) {
		t.Errorf("target error = %v", err)
	}

	f.st.nameTaken = true
	if _, err := f.s.Create(ctx, newID(), f.ws, New{SpaceID: f.space, Name: "dup"}); !errors.Is(err, ErrNameTaken) {
		t.Errorf("duplicate name = %v, want ErrNameTaken", err)
	}
}

func TestUpdate(t *testing.T) {
	ctx := context.Background()
	f := setup()
	q, _ := f.s.Create(ctx, newID(), f.ws, New{SpaceID: f.space, FolderID: f.folder.ID, Name: "a", SQL: "select 1"})

	sql := "select 2"
	got, err := f.s.Update(ctx, newID(), q, Change{SQL: &sql})
	if err != nil || got.Sql != sql || got.Name != "a" || got.FolderID != f.folder.ID || got.Slug != q.Slug {
		t.Fatalf("Update = %+v, %v; unset fields keep their value", got, err)
	}

	got, _ = f.s.Update(ctx, newID(), got, Change{MoveToRoot: true})
	if got.FolderID.Valid {
		t.Error("MoveToRoot clears the folder")
	}
	got, _ = f.s.Update(ctx, newID(), got, Change{MoveToRoot: true, FolderID: &f.folder.ID})
	if got.FolderID != f.folder.ID {
		t.Error("a folder wins over MoveToRoot")
	}

	// Moving to another space keeps a folder of the old space: refused.
	otherSpace := f.other.SpaceID
	if _, err := f.s.Update(ctx, newID(), got, Change{SpaceID: &otherSpace}); !errors.Is(err, ErrFolderNotFound) {
		t.Errorf("move keeping the old folder = %v, want ErrFolderNotFound", err)
	}
	bad := "Bad"
	if _, err := f.s.Update(ctx, newID(), got, Change{Slug: &bad}); !errors.Is(err, ErrInvalidSlug) {
		t.Errorf("bad slug = %v, want ErrInvalidSlug", err)
	}

	items, _ := f.s.List(ctx, f.ws, newID(), Filter{})
	if len(items) != 1 || items[0].Version != 4 {
		t.Errorf("list = %+v, want one query at version 4", items)
	}
	if _, err := f.s.Get(ctx, newID(), q.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("query of another workspace = %v, want ErrNotFound", err)
	}
}
