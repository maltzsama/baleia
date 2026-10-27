package content

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/testdb"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

func pgUUID(id uuid.UUID) pgtype.UUID { return pgtype.UUID{Bytes: id, Valid: true} }

type fixture struct {
	pool  *pgxpool.Pool
	q     db.Querier
	s     *Service
	user  pgtype.UUID
	ws    pgtype.UUID
	space pgtype.UUID
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	pool := testdb.New(t)
	q := db.New(pool)
	ctx := context.Background()
	var user, ws, space pgtype.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO users (name, email, password_hash) VALUES ('u', 'u@x', 'x') RETURNING id`).Scan(&user); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO workspaces (name, type, polaris_catalog_name, owner_user_id) VALUES ('w', 'personal', 'p', $1) RETURNING id`, user).Scan(&ws); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT id FROM spaces WHERE workspace_id = $1 AND is_default`, ws).Scan(&space); err != nil {
		t.Fatal(err)
	}
	return &fixture{pool: pool, q: q, s: New(pool, q), user: user, ws: ws, space: space}
}

func (f *fixture) create(t *testing.T, name, slug string) db.SavedQuery {
	t.Helper()
	ctx := context.Background()
	q, err := f.s.CreateSavedQuery(ctx, f.user, db.CreateSavedQueryParams{
		WorkspaceID: f.ws, SpaceID: f.space, CreatedBy: f.user, UpdatedBy: f.user,
		Name: name, Slug: slug, Sql: "SELECT 1",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	return q
}

func (f *fixture) update(t *testing.T, q db.SavedQuery, sql string) (db.SavedQuery, bool) {
	t.Helper()
	ctx := context.Background()
	out, changed, err := f.s.UpdateSavedQuery(ctx, f.user, db.UpdateSavedQueryParams{
		WorkspaceID: f.ws, ID: q.ID, Name: q.Name, Sql: sql, FolderID: q.FolderID,
		SpaceID: q.SpaceID, Slug: q.Slug, TargetBindingID: q.TargetBindingID,
		TargetSchema: q.TargetSchema, UpdatedBy: f.user,
	}, nil)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	return out, changed
}

func (f *fixture) versions(t *testing.T, id pgtype.UUID) []db.ContentVersion {
	t.Helper()
	rows, err := f.q.ListContentVersions(context.Background(), db.ListContentVersionsParams{
		WorkspaceID: f.ws, EntityType: EntitySavedQuery, EntityID: id, Lim: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	out := make([]db.ContentVersion, 0, len(rows))
	for _, r := range rows {
		out = append(out, db.ContentVersion{
			ID: r.ID, WorkspaceID: r.WorkspaceID, SpaceID: r.SpaceID, EntityType: r.EntityType,
			EntityID: r.EntityID, Version: r.Version, ChangeKind: r.ChangeKind, Snapshot: r.Snapshot,
			AuthorID: r.AuthorID, RestoredFrom: r.RestoredFrom, CreatedAt: r.CreatedAt,
		})
	}
	return out
}

func TestUpdate_OneVersionPerChange(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	q := f.create(t, "Revenue", "revenue")
	if vs := f.versions(t, q.ID); len(vs) != 1 || vs[0].Version != 1 || vs[0].ChangeKind != "create" {
		t.Fatalf("after create: %+v", vs)
	}
	for i := 2; i <= 4; i++ {
		q, changed := f.update(t, q, fmt.Sprintf("SELECT %d", i))
		if !changed {
			t.Fatalf("update %d reported no change", i)
		}
		if vs := f.versions(t, q.ID); len(vs) != i || vs[0].Version != int32(i) {
			t.Fatalf("after update %d: %+v", i, vs)
		}
	}
	// A no-op update writes no version.
	if _, changed := f.update(t, q, "SELECT 4"); changed {
		t.Fatal("no-op update reported a change")
	}
	if vs := f.versions(t, q.ID); len(vs) != 4 {
		t.Fatalf("no-op added a version: %+v", vs)
	}
}

func TestCreate_RollsBackEntityAndVersion(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	missing := pgUUID(uuid.New())
	_, err := f.s.CreateSavedQuery(ctx, f.user, db.CreateSavedQueryParams{
		WorkspaceID: f.ws, SpaceID: missing, CreatedBy: f.user, UpdatedBy: f.user,
		Name: "broken", Slug: "broken", Sql: "SELECT 1",
	})
	if err == nil {
		t.Fatal("create with a missing space should fail")
	}
	var n int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM saved_queries WHERE name = 'broken'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("entity survived a failed create: %d", n)
	}
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM content_versions WHERE entity_type = 'saved_query' AND entity_id NOT IN (SELECT id FROM saved_queries)`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("orphan version survived a failed create: %d", n)
	}
}

func TestDeleteThenRestoreSameID(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	q := f.create(t, "Revenue", "revenue")
	if err := f.s.DeleteSavedQuery(ctx, f.user, f.ws, q.ID); err != nil {
		t.Fatal(err)
	}
	trash, err := f.q.ListDeletedContent(ctx, db.ListDeletedContentParams{WorkspaceID: f.ws, EntityType: ptr("saved_query"), Lim: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(trash) != 1 || trash[0].EntityID.Bytes != q.ID.Bytes {
		t.Fatalf("trash = %+v", trash)
	}
	restored, err := f.s.RestoreSavedQuery(ctx, f.user, f.ws, q.ID, 1, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if restored.ID.Bytes != q.ID.Bytes {
		t.Fatalf("restored id = %v, want %v", restored.ID, q.ID)
	}
	if restored.Name != "Revenue" {
		t.Fatalf("restored name = %q", restored.Name)
	}
	vs := f.versions(t, q.ID)
	if vs[0].ChangeKind != "restore" || vs[0].RestoredFrom == nil || *vs[0].RestoredFrom != 1 {
		t.Fatalf("restore version = %+v", vs[0])
	}
}

func TestRestoreNameConflict(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	q := f.create(t, "Revenue", "revenue")
	if err := f.s.DeleteSavedQuery(ctx, f.user, f.ws, q.ID); err != nil {
		t.Fatal(err)
	}
	f.create(t, "Revenue", "revenue_2")
	if _, err := f.s.RestoreSavedQuery(ctx, f.user, f.ws, q.ID, 1, nil, nil); !errors.Is(err, ErrNameConflict) {
		t.Fatalf("restore conflict = %v, want ErrNameConflict", err)
	}
	newName := "Revenue restored"
	restored, err := f.s.RestoreSavedQuery(ctx, f.user, f.ws, q.ID, 1, &newName, nil)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Name != newName {
		t.Fatalf("restored name = %q", restored.Name)
	}
}

func TestConcurrentUpdates_NoDuplicateVersion(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	q := f.create(t, "Concurrent", "concurrent")
	const n = 20
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _, err := f.s.UpdateSavedQuery(ctx, f.user, db.UpdateSavedQueryParams{
				// Never "SELECT 1" (the created SQL): that update would be a
				// no-op when it runs first and write no version.
				WorkspaceID: f.ws, ID: q.ID, Name: q.Name, Sql: fmt.Sprintf("SELECT %d", 100+i),
				SpaceID: q.SpaceID, Slug: q.Slug, UpdatedBy: f.user,
			}, nil)
			if err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent update: %v", err)
	}
	vs := f.versions(t, q.ID)
	if len(vs) != n+1 {
		t.Fatalf("versions = %d, want %d", len(vs), n+1)
	}
	seen := map[int32]bool{}
	for _, v := range vs {
		if seen[v.Version] {
			t.Fatalf("duplicate version %d", v.Version)
		}
		seen[v.Version] = true
	}
}

func ptr[T any](v T) *T { return &v }

func TestUpdate_VersionConflict(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	q := f.create(t, "conflict", "conflict")

	next, changed, err := f.s.UpdateSavedQuery(ctx, f.user, db.UpdateSavedQueryParams{
		WorkspaceID: f.ws, ID: q.ID, Name: q.Name, Sql: "SELECT 2",
		SpaceID: q.SpaceID, Slug: q.Slug, UpdatedBy: f.user,
	}, int32Ptr(1))
	if err != nil || !changed {
		t.Fatalf("matching expected_version: changed=%v err=%v", changed, err)
	}
	if next.Sql != "SELECT 2" {
		t.Fatalf("update = %+v", next)
	}

	_, _, err = f.s.UpdateSavedQuery(ctx, f.user, db.UpdateSavedQueryParams{
		WorkspaceID: f.ws, ID: q.ID, Name: q.Name, Sql: "SELECT 3",
		SpaceID: q.SpaceID, Slug: q.Slug, UpdatedBy: f.user,
	}, int32Ptr(1))
	var vc *VersionConflict
	if !errors.As(err, &vc) {
		t.Fatalf("stale expected_version: err = %v, want VersionConflict", err)
	}
	if vc.Current != 2 {
		t.Fatalf("current version = %d, want 2", vc.Current)
	}
	if !errors.Is(err, ErrVersionConflict) {
		t.Fatal("VersionConflict must wrap ErrVersionConflict")
	}
}

func int32Ptr(v int32) *int32 { return &v }
