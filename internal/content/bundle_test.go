package content_test

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/galileostudio/baleia/internal/content"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/dialect"
	"github.com/galileostudio/baleia/internal/store/model"
	"github.com/galileostudio/baleia/internal/store/repo"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

func (f *fixture) workspace(t *testing.T, name, slug string) pgtype.UUID {
	t.Helper()
	ws, err := f.q.CreateWorkspace(context.Background(), db.CreateWorkspaceParams{
		Name: name, Type: "personal", PolarisCatalogName: slug, OwnerUserID: f.user,
	})
	if err != nil {
		t.Fatal(err)
	}
	return ws.ID
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
	conn, err := f.q.CreateCatalogConnection(ctx, db.CreateCatalogConnectionParams{
		ID: pgUUID(uuid.New()), Name: "conn", Type: "polaris", Url: "http://p", CreatedBy: f.user,
	})
	if err != nil {
		t.Fatal(err)
	}
	cat := model.Catalog{
		ID: model.UUID(repo.NewUUID()), ConnectionID: model.UUID(repo.UUIDString(conn.ID)),
		Name: model.Text(name), Origin: "native",
	}
	if err := f.gdb.Create(&cat).Error; err != nil {
		t.Fatal(err)
	}
	// Catalog creation grants the creator's workspace (ADR 0018 §4): the
	// personal auto-reach rule is gone, so a catalog is only reachable by grant.
	f.grant(t, f.ws, repo.PGUUID(string(cat.ID)))
	return repo.PGUUID(string(cat.ID))
}

func (f *fixture) grant(t *testing.T, ws, catalog pgtype.UUID) {
	t.Helper()
	if _, err := f.q.UpsertWorkspaceCatalogGrant(context.Background(), db.UpsertWorkspaceCatalogGrantParams{
		WorkspaceID: ws, CatalogID: catalog, GrantedBy: f.user,
	}); err != nil {
		t.Fatal(err)
	}
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
	t.Parallel()
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
	if a.Items[len(a.Items)-1].Kind != content.KindSemanticMetric {
		t.Fatalf("metrics should come last: %+v", a.Items)
	}
}

func TestRoundTripNoChanges(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	source := f.spaceNamed(t, "Finance", "finance")
	catalog := f.grantedCatalog(t, "lake")
	f.seed(t, source, catalog)

	ws2 := f.workspace(t, "Other", "other")
	f.grant(t, ws2, catalog)
	dest := f.spaceIn(t, ws2, "Finance copy", "finance_copy")

	bundle, err := f.s.Export(context.Background(), f.ws, source, "dev")
	if err != nil {
		t.Fatal(err)
	}
	report, err := f.s.Import(context.Background(), content.ImportOptions{Dialect: dialect.NewRegistry().MustGet(dialect.Trino),
		WorkspaceID: ws2, SpaceID: dest, Actor: f.user, Bundle: bundle, Mode: content.ImportApply,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !report.OK {
		t.Fatalf("first import: %+v", report)
	}
	for _, it := range report.Items {
		if it.Action != content.ActionCreate {
			t.Fatalf("first import action = %s for %s/%s, want CREATE", it.Action, it.Kind, it.Slug)
		}
	}

	report, err = f.s.Import(context.Background(), content.ImportOptions{Dialect: dialect.NewRegistry().MustGet(dialect.Trino),
		WorkspaceID: ws2, SpaceID: dest, Actor: f.user, Bundle: bundle, Mode: content.ImportApply,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range report.Items {
		if it.Action != content.ActionNoChanges {
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
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	source := f.spaceNamed(t, "Finance", "finance")
	catalog := f.grantedCatalog(t, "lake")
	f.createIn(t, f.ws, source, "Revenue", "revenue", "SELECT 1")
	f.metric(t, source, catalog, "net_revenue", "Net revenue")

	ws2 := f.workspace(t, "Other", "other")
	f.grant(t, ws2, catalog)
	dest := f.spaceIn(t, ws2, "Finance copy", "finance_copy")

	bundle, _ := f.s.Export(ctx, f.ws, source, "dev")
	if _, err := f.s.Import(ctx, content.ImportOptions{Dialect: dialect.NewRegistry().MustGet(dialect.Trino), WorkspaceID: ws2, SpaceID: dest, Actor: f.user, Bundle: bundle, Mode: content.ImportApply}); err != nil {
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

	report, err := f.s.Import(ctx, content.ImportOptions{Dialect: dialect.NewRegistry().MustGet(dialect.Trino), WorkspaceID: ws2, SpaceID: dest, Actor: f.user, Bundle: bundle, Mode: content.ImportApply})
	if err != nil {
		t.Fatal(err)
	}
	updated := false
	for _, it := range report.Items {
		if it.Kind == content.KindSavedQuery && it.Slug == "revenue" {
			updated = it.Action == content.ActionUpdate
		}
	}
	if !updated {
		t.Fatalf("changed SQL was not an UPDATE: %+v", report.Items)
	}
	rows, err := f.q.ListContentVersions(ctx, db.ListContentVersionsParams{
		WorkspaceID: ws2, EntityType: content.EntitySavedQuery, EntityID: destQuery.ID, Lim: 10,
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
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	source := f.spaceNamed(t, "Finance", "finance")
	catalog := f.grantedCatalog(t, "lake")
	f.seed(t, source, catalog)
	ws2 := f.workspace(t, "Other", "other")
	f.grant(t, ws2, catalog)
	dest := f.spaceIn(t, ws2, "Finance copy", "finance_copy")

	bundle, _ := f.s.Export(ctx, f.ws, source, "dev")
	if _, err := f.s.Import(ctx, content.ImportOptions{Dialect: dialect.NewRegistry().MustGet(dialect.Trino), WorkspaceID: ws2, SpaceID: dest, Actor: f.user, Bundle: bundle, Mode: content.ImportApply}); err != nil {
		t.Fatal(err)
	}
	partial := content.Bundle{Format: content.BundleFormat, FormatVersion: content.BundleVersion, BaleiaVersion: "dev",
		Space: bundle.Space, Items: []content.BundleItem{{Kind: content.KindFolder, Slug: "monthly", Name: "Monthly"}}}
	if _, err := f.s.Import(ctx, content.ImportOptions{Dialect: dialect.NewRegistry().MustGet(dialect.Trino), WorkspaceID: ws2, SpaceID: dest, Actor: f.user, Bundle: partial, Mode: content.ImportApply}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.q.GetSavedQueryBySlug(ctx, db.GetSavedQueryBySlugParams{WorkspaceID: ws2, Slug: "revenue"}); err != nil {
		t.Fatalf("omitted query was deleted: %v", err)
	}
}

func TestUnknownKindAndForbiddenSQL(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	dest := f.spaceNamed(t, "Dest", "dest")
	bundle := content.Bundle{Format: content.BundleFormat, FormatVersion: content.BundleVersion, BaleiaVersion: "dev",
		Space: content.BundleSpace{Slug: "dest", Name: "Dest"},
		Items: []content.BundleItem{
			{Kind: "model", Slug: "nope"},
			{Kind: content.KindSavedQuery, Slug: "ddl", Name: "DDL", SQL: "CREATE CATALOG x WITH (a = 'b')"},
		}}
	report, err := f.s.Import(context.Background(), content.ImportOptions{Dialect: dialect.NewRegistry().MustGet(dialect.Trino), WorkspaceID: f.ws, SpaceID: dest, Actor: f.user, Bundle: bundle, Mode: content.ImportApply})
	if !errors.Is(err, content.ErrImportFailed) {
		t.Fatalf("apply with errors = %v, want content.ErrImportFailed", err)
	}
	if report.OK {
		t.Fatalf("report should not be ok: %+v", report)
	}
	for _, it := range report.Items {
		if it.Action != content.ActionError {
			t.Fatalf("%s/%s = %s, want ERROR", it.Kind, it.Slug, it.Action)
		}
	}
	var n int64
	if err := f.gdb.Model(&model.SavedQuery{}).Where("slug = ?", "ddl").Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("forbidden SQL was written: %d", n)
	}
}

func TestDryRunWritesNothing(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	dest := f.spaceNamed(t, "Dest", "dest")
	bundle := content.Bundle{Format: content.BundleFormat, FormatVersion: content.BundleVersion, BaleiaVersion: "dev",
		Space: content.BundleSpace{Slug: "dest", Name: "Dest"},
		Items: []content.BundleItem{
			{Kind: content.KindFolder, Slug: "f", Name: "F"},
			{Kind: content.KindSavedQuery, Slug: "q", Name: "Q", SQL: "SELECT 1", Folder: ptr("f")},
		}}
	report, err := f.s.Import(context.Background(), content.ImportOptions{Dialect: dialect.NewRegistry().MustGet(dialect.Trino), WorkspaceID: f.ws, SpaceID: dest, Actor: f.user, Bundle: bundle, Mode: content.ImportDryRun})
	if err != nil {
		t.Fatal(err)
	}
	if !report.OK || report.Items[0].Action != content.ActionCreate {
		t.Fatalf("dry run report = %+v", report)
	}
	var folders, queries int64
	if err := f.gdb.Model(&model.QueryFolder{}).Where("space_id = ?", repo.UUIDString(dest)).Count(&folders).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.gdb.Model(&model.SavedQuery{}).Where("space_id = ?", repo.UUIDString(dest)).Count(&queries).Error; err != nil {
		t.Fatal(err)
	}
	if folders != 0 || queries != 0 {
		t.Fatalf("dry run wrote folders=%d queries=%d", folders, queries)
	}
}

func TestDependencyOrder(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	dest := f.spaceNamed(t, "Dest", "dest")
	bundle := content.Bundle{Format: content.BundleFormat, FormatVersion: content.BundleVersion, BaleiaVersion: "dev",
		Space: content.BundleSpace{Slug: "dest", Name: "Dest"},
		Items: []content.BundleItem{
			// The query references the folder before it is defined.
			{Kind: content.KindSavedQuery, Slug: "q", Name: "Q", SQL: "SELECT 1", Folder: ptr("child")},
			{Kind: content.KindFolder, Slug: "child", Name: "Child", Parent: ptr("parent")},
			{Kind: content.KindFolder, Slug: "parent", Name: "Parent"},
		}}
	report, err := f.s.Import(context.Background(), content.ImportOptions{Dialect: dialect.NewRegistry().MustGet(dialect.Trino), WorkspaceID: f.ws, SpaceID: dest, Actor: f.user, Bundle: bundle, Mode: content.ImportApply})
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

// TestImport_RequiresDialect: without a dialect the import refuses up front
// instead of panicking on the first saved query.
func TestImport_RequiresDialect(t *testing.T) {
	t.Parallel()
	s := content.New(nil, nil)
	if _, err := s.Import(context.Background(), content.ImportOptions{Mode: content.ImportDryRun}); !errors.Is(err, content.ErrNoDialect) {
		t.Fatalf("err = %v, want content.ErrNoDialect", err)
	}
}
