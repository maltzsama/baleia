package content

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/jackc/pgx/v5/pgtype"
)

func (f *fixture) workspace(t *testing.T, name, slug string) pgtype.UUID {
	t.Helper()
	var ws pgtype.UUID
	if err := f.pool.QueryRow(context.Background(),
		`INSERT INTO workspaces (name, type, polaris_catalog_name, owner_user_id) VALUES ($1, 'personal', $2, $3) RETURNING id`,
		name, slug, f.user).Scan(&ws); err != nil {
		t.Fatal(err)
	}
	return ws
}

func (f *fixture) spaceIn(t *testing.T, ws pgtype.UUID, name, slug string) pgtype.UUID {
	t.Helper()
	s, err := f.q.CreateSpace(context.Background(), db.CreateSpaceParams{
		WorkspaceID: ws, Name: name, Slug: slug, Visibility: "workspace",
		DefaultRole: "editor", CreatedBy: f.user,
	})
	if err != nil {
		t.Fatal(err)
	}
	return s.ID
}

func (f *fixture) spaceNamed(t *testing.T, name, slug string) pgtype.UUID {
	return f.spaceIn(t, f.ws, name, slug)
}

func (f *fixture) grantedCatalog(t *testing.T, name string) pgtype.UUID {
	t.Helper()
	ctx := context.Background()
	var conn, cat pgtype.UUID
	if err := f.pool.QueryRow(ctx, `INSERT INTO catalog_connections (name, type, url, created_by) VALUES ('conn', 'polaris', 'http://p', $1) RETURNING id`, f.user).Scan(&conn); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(ctx, `INSERT INTO catalogs (connection_id, name, origin) VALUES ($1, $2, 'native') RETURNING id`, conn, name).Scan(&cat); err != nil {
		t.Fatal(err)
	}
	return cat
}

func (f *fixture) folder(t *testing.T, space pgtype.UUID, name, slug string, parent pgtype.UUID) db.QueryFolder {
	t.Helper()
	folder, err := f.q.CreateQueryFolder(context.Background(), db.CreateQueryFolderParams{
		WorkspaceID: f.ws, SpaceID: space, ParentID: parent, Name: name, Slug: slug,
	})
	if err != nil {
		t.Fatal(err)
	}
	return folder
}

func (f *fixture) createIn(t *testing.T, ws, space pgtype.UUID, name, slug, sql string) db.SavedQuery {
	t.Helper()
	q, err := f.s.CreateSavedQuery(context.Background(), f.user, db.CreateSavedQueryParams{
		WorkspaceID: ws, SpaceID: space, CreatedBy: f.user, UpdatedBy: f.user,
		Name: name, Slug: slug, Sql: sql,
	})
	if err != nil {
		t.Fatal(err)
	}
	return q
}

func (f *fixture) metric(t *testing.T, space, catalog pgtype.UUID, slug, label string) db.SemanticMetric {
	t.Helper()
	m, err := f.s.CreateSemanticMetric(context.Background(), f.user, db.CreateSemanticMetricParams{
		WorkspaceID: f.ws, SpaceID: space, Slug: slug, Label: label, CatalogID: catalog,
		Namespace: []string{"sales"}, TableName: "orders",
		Definition: []byte(`{"type":"simple","aggregation":"count"}`), FormatVersion: 1, UpdatedBy: f.user,
	})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func (f *fixture) seed(t *testing.T, space, catalog pgtype.UUID) {
	t.Helper()
	monthly := f.folder(t, space, "Monthly", "monthly", pgtype.UUID{})
	f.folder(t, space, "Weekly", "weekly", monthly.ID)
	f.createIn(t, f.ws, space, "Revenue", "revenue", "SELECT 1")
	f.metric(t, space, catalog, "net_revenue", "Net revenue")
}

func TestExportDeterministic(t *testing.T) {
	f := newFixture(t)
	space := f.spaceNamed(t, "Finance", "finance")
	catalog := f.grantedCatalog(t, "lake")
	f.seed(t, space, catalog)

	a, err := f.s.Export(context.Background(), f.ws, space, "dev")
	if err != nil {
		t.Fatal(err)
	}
	b, err := f.s.Export(context.Background(), f.ws, space, "dev")
	if err != nil {
		t.Fatal(err)
	}
	ra, _ := a.Marshal()
	rb, _ := b.Marshal()
	if !bytes.Equal(ra, rb) {
		t.Fatalf("exports differ:\n%s\n%s", ra, rb)
	}
	kindOrder := map[string]int{}
	for i, it := range a.Items {
		kindOrder[it.Kind+":"+it.Slug] = i
	}
	if kindOrder["folder:monthly"] > kindOrder["folder:weekly"] {
		t.Fatalf("parent folder exported after its child: %+v", a.Items)
	}
	if kindOrder["saved_query:revenue"] < kindOrder["folder:monthly"] {
		t.Fatalf("query exported before folders: %+v", a.Items)
	}
	if a.Items[len(a.Items)-1].Kind != KindSemanticMetric {
		t.Fatalf("metrics should come last: %+v", a.Items)
	}
}

func TestRoundTripNoChanges(t *testing.T) {
	f := newFixture(t)
	source := f.spaceNamed(t, "Finance", "finance")
	catalog := f.grantedCatalog(t, "lake")
	f.seed(t, source, catalog)

	ws2 := f.workspace(t, "Other", "other")
	dest := f.spaceIn(t, ws2, "Finance copy", "finance_copy")

	bundle, err := f.s.Export(context.Background(), f.ws, source, "dev")
	if err != nil {
		t.Fatal(err)
	}
	report, err := f.s.Import(context.Background(), ImportOptions{
		WorkspaceID: ws2, SpaceID: dest, Actor: f.user, Bundle: bundle, Mode: ImportApply,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !report.OK {
		t.Fatalf("first import: %+v", report)
	}
	for _, it := range report.Items {
		if it.Action != ActionCreate {
			t.Fatalf("first import action = %s for %s/%s, want CREATE", it.Action, it.Kind, it.Slug)
		}
	}

	report, err = f.s.Import(context.Background(), ImportOptions{
		WorkspaceID: ws2, SpaceID: dest, Actor: f.user, Bundle: bundle, Mode: ImportApply,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range report.Items {
		if it.Action != ActionNoChanges {
			t.Fatalf("re-import action = %s for %s/%s, want NO_CHANGES", it.Action, it.Kind, it.Slug)
		}
	}

	exported, err := f.s.Export(context.Background(), ws2, dest, "dev")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(exported.Items, bundle.Items) {
		t.Fatalf("round trip changed the items:\n%+v\n%+v", exported.Items, bundle.Items)
	}
}

func TestUpdateCreatesImportVersion(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	source := f.spaceNamed(t, "Finance", "finance")
	catalog := f.grantedCatalog(t, "lake")
	f.createIn(t, f.ws, source, "Revenue", "revenue", "SELECT 1")
	f.metric(t, source, catalog, "net_revenue", "Net revenue")

	ws2 := f.workspace(t, "Other", "other")
	dest := f.spaceIn(t, ws2, "Finance copy", "finance_copy")

	bundle, _ := f.s.Export(ctx, f.ws, source, "dev")
	if _, err := f.s.Import(ctx, ImportOptions{WorkspaceID: ws2, SpaceID: dest, Actor: f.user, Bundle: bundle, Mode: ImportApply}); err != nil {
		t.Fatal(err)
	}

	destQuery, err := f.q.GetSavedQueryBySlug(ctx, db.GetSavedQueryBySlugParams{WorkspaceID: ws2, Slug: "revenue"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.q.UpdateSavedQuery(ctx, db.UpdateSavedQueryParams{
		WorkspaceID: ws2, ID: destQuery.ID, Name: destQuery.Name, Sql: "SELECT 99",
		SpaceID: dest, Slug: destQuery.Slug, UpdatedBy: f.user,
	}); err != nil {
		t.Fatal(err)
	}

	report, err := f.s.Import(ctx, ImportOptions{WorkspaceID: ws2, SpaceID: dest, Actor: f.user, Bundle: bundle, Mode: ImportApply})
	if err != nil {
		t.Fatal(err)
	}
	updated := false
	for _, it := range report.Items {
		if it.Kind == KindSavedQuery && it.Slug == "revenue" {
			updated = it.Action == ActionUpdate
		}
	}
	if !updated {
		t.Fatalf("changed SQL was not an UPDATE: %+v", report.Items)
	}
	rows, err := f.q.ListContentVersions(ctx, db.ListContentVersionsParams{
		WorkspaceID: ws2, EntityType: EntitySavedQuery, EntityID: destQuery.ID, Lim: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range rows {
		if r.ChangeKind == "import" {
			found = true
		}
	}
	if !found {
		t.Fatalf("import did not write an import version: %+v", rows)
	}
}

func TestOmittedItemsNotDeleted(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	source := f.spaceNamed(t, "Finance", "finance")
	catalog := f.grantedCatalog(t, "lake")
	f.seed(t, source, catalog)
	ws2 := f.workspace(t, "Other", "other")
	dest := f.spaceIn(t, ws2, "Finance copy", "finance_copy")

	bundle, _ := f.s.Export(ctx, f.ws, source, "dev")
	if _, err := f.s.Import(ctx, ImportOptions{WorkspaceID: ws2, SpaceID: dest, Actor: f.user, Bundle: bundle, Mode: ImportApply}); err != nil {
		t.Fatal(err)
	}
	partial := Bundle{Format: BundleFormat, FormatVersion: BundleVersion, BaleiaVersion: "dev",
		Space: bundle.Space, Items: []BundleItem{{Kind: KindFolder, Slug: "monthly", Name: "Monthly"}}}
	if _, err := f.s.Import(ctx, ImportOptions{WorkspaceID: ws2, SpaceID: dest, Actor: f.user, Bundle: partial, Mode: ImportApply}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.q.GetSavedQueryBySlug(ctx, db.GetSavedQueryBySlugParams{WorkspaceID: ws2, Slug: "revenue"}); err != nil {
		t.Fatalf("omitted query was deleted: %v", err)
	}
}

func TestUnknownKindAndForbiddenSQL(t *testing.T) {
	f := newFixture(t)
	dest := f.spaceNamed(t, "Dest", "dest")
	bundle := Bundle{Format: BundleFormat, FormatVersion: BundleVersion, BaleiaVersion: "dev",
		Space: BundleSpace{Slug: "dest", Name: "Dest"},
		Items: []BundleItem{
			{Kind: "model", Slug: "nope"},
			{Kind: KindSavedQuery, Slug: "ddl", Name: "DDL", SQL: "CREATE CATALOG x WITH (a = 'b')"},
		}}
	report, err := f.s.Import(context.Background(), ImportOptions{WorkspaceID: f.ws, SpaceID: dest, Actor: f.user, Bundle: bundle, Mode: ImportApply})
	if !errors.Is(err, ErrImportFailed) {
		t.Fatalf("apply with errors = %v, want ErrImportFailed", err)
	}
	if report.OK {
		t.Fatalf("report should not be ok: %+v", report)
	}
	for _, it := range report.Items {
		if it.Action != ActionError {
			t.Fatalf("%s/%s = %s, want ERROR", it.Kind, it.Slug, it.Action)
		}
	}
	var n int
	if err := f.pool.QueryRow(context.Background(), `SELECT count(*) FROM saved_queries WHERE slug = 'ddl'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("forbidden SQL was written: %d", n)
	}
}

func TestDryRunWritesNothing(t *testing.T) {
	f := newFixture(t)
	dest := f.spaceNamed(t, "Dest", "dest")
	bundle := Bundle{Format: BundleFormat, FormatVersion: BundleVersion, BaleiaVersion: "dev",
		Space: BundleSpace{Slug: "dest", Name: "Dest"},
		Items: []BundleItem{
			{Kind: KindFolder, Slug: "f", Name: "F"},
			{Kind: KindSavedQuery, Slug: "q", Name: "Q", SQL: "SELECT 1", Folder: ptr("f")},
		}}
	report, err := f.s.Import(context.Background(), ImportOptions{WorkspaceID: f.ws, SpaceID: dest, Actor: f.user, Bundle: bundle, Mode: ImportDryRun})
	if err != nil {
		t.Fatal(err)
	}
	if !report.OK || report.Items[0].Action != ActionCreate {
		t.Fatalf("dry run report = %+v", report)
	}
	var folders, queries int
	if err := f.pool.QueryRow(context.Background(), `SELECT count(*) FROM query_folders WHERE space_id = $1`, dest).Scan(&folders); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(context.Background(), `SELECT count(*) FROM saved_queries WHERE space_id = $1`, dest).Scan(&queries); err != nil {
		t.Fatal(err)
	}
	if folders != 0 || queries != 0 {
		t.Fatalf("dry run wrote folders=%d queries=%d", folders, queries)
	}
}

func TestDependencyOrder(t *testing.T) {
	f := newFixture(t)
	dest := f.spaceNamed(t, "Dest", "dest")
	bundle := Bundle{Format: BundleFormat, FormatVersion: BundleVersion, BaleiaVersion: "dev",
		Space: BundleSpace{Slug: "dest", Name: "Dest"},
		Items: []BundleItem{
			// The query references the folder before it is defined.
			{Kind: KindSavedQuery, Slug: "q", Name: "Q", SQL: "SELECT 1", Folder: ptr("child")},
			{Kind: KindFolder, Slug: "child", Name: "Child", Parent: ptr("parent")},
			{Kind: KindFolder, Slug: "parent", Name: "Parent"},
		}}
	report, err := f.s.Import(context.Background(), ImportOptions{WorkspaceID: f.ws, SpaceID: dest, Actor: f.user, Bundle: bundle, Mode: ImportApply})
	if err != nil {
		t.Fatal(err)
	}
	if !report.OK {
		t.Fatalf("report = %+v", report)
	}
	q, err := f.q.GetSavedQueryBySlug(context.Background(), db.GetSavedQueryBySlugParams{WorkspaceID: f.ws, Slug: "q"})
	if err != nil {
		t.Fatal(err)
	}
	folder, err := f.q.GetFolderBySlug(context.Background(), db.GetFolderBySlugParams{SpaceID: dest, Slug: "child"})
	if err != nil {
		t.Fatal(err)
	}
	if !q.FolderID.Valid || q.FolderID.Bytes != folder.ID.Bytes {
		t.Fatalf("query folder = %v, want %v", q.FolderID, folder.ID)
	}
}
