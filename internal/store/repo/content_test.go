package repo

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/content"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/store/model"
	"github.com/galileostudio/baleia/internal/testdb"
	"gorm.io/gorm"
)

// contentFixture is a workspace with its default space, a user, and a granted
// catalog bound to an engine, enough for the content service to write saved
// queries, metrics, models and bundles.
type contentFixture struct {
	gdb     *gorm.DB
	r       *Repo
	user    model.UUID
	ws      model.UUID
	space   model.UUID
	catalog model.UUID
	binding model.UUID
}

func newContentFixture(t *testing.T) *contentFixture {
	t.Helper()
	gdb := testdb.New(t)
	r := New(gdb)
	ctx := context.Background()

	user := createAPIKeyUser(t, gdb, "content@example.test")
	userID := PGUUID(string(user.ID))
	ws, err := r.CreateWorkspace(ctx, db.CreateWorkspaceParams{
		Name: "Content", Type: "personal", PolarisCatalogName: "ws_content_test", OwnerUserID: userID,
	})
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	space, err := r.GetDefaultSpace(ctx, ws.ID)
	if err != nil {
		t.Fatalf("GetDefaultSpace: %v", err)
	}

	now := time.Now().UTC()
	conn := model.CatalogConnection{
		ID: model.UUID(NewUUID()), Name: model.Text("content-conn-" + NewUUID()[:8]),
		Type: "polaris", URL: "http://polaris.test", CreatedAt: now, UpdatedAt: now,
	}
	if err := gdb.Create(&conn).Error; err != nil {
		t.Fatalf("create connection: %v", err)
	}
	catalog := model.Catalog{
		ID: model.UUID(NewUUID()), ConnectionID: conn.ID, Name: "lake", Origin: "native",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := gdb.Create(&catalog).Error; err != nil {
		t.Fatalf("create catalog: %v", err)
	}
	grant := model.WorkspaceCatalogGrant{
		WorkspaceID: model.UUID(UUIDString(ws.ID)), CatalogID: catalog.ID,
		GrantedBy: user.ID, GrantedAt: now,
	}
	if err := gdb.Create(&grant).Error; err != nil {
		t.Fatalf("create grant: %v", err)
	}

	engineConn := model.CatalogConnection{
		ID: model.UUID(NewUUID()), Name: model.Text("content-engine-" + NewUUID()[:8]),
		Type: "trino", URL: "http://trino.test", CreatedAt: now, UpdatedAt: now,
	}
	if err := gdb.Create(&engineConn).Error; err != nil {
		t.Fatalf("create engine connection: %v", err)
	}
	engine := model.Engine{
		ID: model.UUID(NewUUID()), Name: model.Text("trino"), Kind: "trino", Class: "federated",
		ConnectionID: engineConn.ID, CreatedAt: now, UpdatedAt: now,
	}
	if err := gdb.Create(&engine).Error; err != nil {
		t.Fatalf("create engine: %v", err)
	}
	binding := model.CatalogBinding{
		ID: model.UUID(NewUUID()), CatalogID: catalog.ID, EngineID: engine.ID,
		EngineCatalogName: "lake_t", Mode: "adopted", CreatedAt: now, UpdatedAt: now,
	}
	if err := gdb.Create(&binding).Error; err != nil {
		t.Fatalf("create binding: %v", err)
	}

	return &contentFixture{
		gdb: gdb, r: r, user: user.ID, ws: model.UUID(UUIDString(ws.ID)),
		space: model.UUID(UUIDString(space.ID)), catalog: catalog.ID, binding: binding.ID,
	}
}

func (f *contentFixture) workspaceID() db.UUID { return PGUUID(string(f.ws)) }
func (f *contentFixture) spaceID() db.UUID     { return PGUUID(string(f.space)) }
func (f *contentFixture) actor() db.UUID       { return PGUUID(string(f.user)) }
func (f *contentFixture) catalogID() db.UUID   { return PGUUID(string(f.catalog)) }
func (f *contentFixture) bindingID() db.UUID   { return PGUUID(string(f.binding)) }

func (f *contentFixture) versions(t *testing.T, entityType string, id db.UUID) []db.ListContentVersionsRow {
	t.Helper()
	rows, err := f.r.ListContentVersions(context.Background(), db.ListContentVersionsParams{
		WorkspaceID: f.workspaceID(), EntityType: entityType, EntityID: id, Lim: 100,
	})
	if err != nil {
		t.Fatalf("ListContentVersions: %v", err)
	}
	return rows
}

// TestContentSavedQueryLifecycle drives the saved-query service over the GORM
// store: create with version 1, update with a new version, a no-op writing
// none, delete recording a version and removing the row, the trash listing,
// and a restore recreating the row with its original id.
func TestContentSavedQueryLifecycle(t *testing.T) {
	t.Parallel()
	f := newContentFixture(t)
	ctx := context.Background()
	svc := content.New(f.r, f.r.TxContent)

	q, err := svc.CreateSavedQuery(ctx, f.actor(), db.CreateSavedQueryParams{
		WorkspaceID: f.workspaceID(), SpaceID: f.spaceID(), CreatedBy: f.actor(), UpdatedBy: f.actor(),
		Name: "Revenue", Slug: "revenue", Sql: "SELECT 1", TargetBindingID: f.bindingID(),
	})
	if err != nil {
		t.Fatalf("CreateSavedQuery: %v", err)
	}
	if !q.ID.Valid || q.Name != "Revenue" {
		t.Fatalf("created = %+v", q)
	}

	versions := f.versions(t, content.EntitySavedQuery, q.ID)
	if len(versions) != 1 || versions[0].Version != 1 || versions[0].ChangeKind != "create" {
		t.Fatalf("after create: %+v", versions)
	}
	latest, err := f.r.LatestContentVersion(ctx, db.LatestContentVersionParams{
		WorkspaceID: f.workspaceID(), EntityType: content.EntitySavedQuery, EntityID: q.ID,
	})
	if err != nil || latest.Version != 1 {
		t.Fatalf("LatestContentVersion = %+v, %v", latest, err)
	}
	one, err := f.r.GetContentVersion(ctx, db.GetContentVersionParams{
		WorkspaceID: f.workspaceID(), EntityType: content.EntitySavedQuery, EntityID: q.ID, Version: 1,
	})
	if err != nil {
		t.Fatalf("GetContentVersion: %v", err)
	}
	if one.AuthorID != q.CreatedBy || len(one.Snapshot) == 0 {
		t.Fatalf("version 1 = %+v", one)
	}
	if snap, err := content.ParseSavedQuerySnapshot(one.Snapshot); err != nil || snap.SQL != "SELECT 1" {
		t.Fatalf("snapshot = %+v, %v", snap, err)
	}

	update := db.UpdateSavedQueryParams{
		WorkspaceID: f.workspaceID(), ID: q.ID, Name: q.Name, Sql: "SELECT 2",
		FolderID: q.FolderID, SpaceID: q.SpaceID, Slug: q.Slug,
		TargetBindingID: q.TargetBindingID, TargetSchema: q.TargetSchema, UpdatedBy: f.actor(),
	}
	out, changed, err := svc.UpdateSavedQuery(ctx, f.actor(), update, nil)
	if err != nil || !changed || out.Sql != "SELECT 2" {
		t.Fatalf("update = %+v, changed=%v, err=%v", out, changed, err)
	}
	if versions = f.versions(t, content.EntitySavedQuery, q.ID); len(versions) != 2 || versions[0].ChangeKind != "update" {
		t.Fatalf("after update: %+v", versions)
	}

	if _, changed, err = svc.UpdateSavedQuery(ctx, f.actor(), update, nil); err != nil || changed {
		t.Fatalf("no-op update: changed=%v err=%v", changed, err)
	}
	if versions = f.versions(t, content.EntitySavedQuery, q.ID); len(versions) != 2 {
		t.Fatalf("no-op wrote a version: %+v", versions)
	}

	if err := svc.DeleteSavedQuery(ctx, f.actor(), f.workspaceID(), q.ID); err != nil {
		t.Fatalf("DeleteSavedQuery: %v", err)
	}
	if _, err := f.r.GetSavedQueryByID(ctx, db.GetSavedQueryByIDParams{ID: q.ID, WorkspaceID: f.workspaceID()}); !errors.Is(err, db.ErrNoRows) {
		t.Fatalf("deleted row = %v, want db.ErrNoRows", err)
	}
	if versions = f.versions(t, content.EntitySavedQuery, q.ID); len(versions) != 3 || versions[0].ChangeKind != "delete" {
		t.Fatalf("after delete: %+v", versions)
	}

	trash, err := f.r.ListDeletedContent(ctx, db.ListDeletedContentParams{
		WorkspaceID: f.workspaceID(), Lim: 10,
	})
	if err != nil {
		t.Fatalf("ListDeletedContent: %v", err)
	}
	if len(trash) != 1 || trash[0].EntityID != q.ID || trash[0].SpaceName == nil || *trash[0].SpaceName == "" {
		t.Fatalf("trash = %+v", trash)
	}

	restored, err := svc.RestoreSavedQuery(ctx, f.actor(), f.workspaceID(), q.ID, 1, nil, nil)
	if err != nil {
		t.Fatalf("RestoreSavedQuery: %v", err)
	}
	if restored.ID != q.ID || restored.Name != "Revenue" || restored.Sql != "SELECT 1" {
		t.Fatalf("restored = %+v", restored)
	}
	versions = f.versions(t, content.EntitySavedQuery, q.ID)
	if len(versions) != 4 || versions[0].ChangeKind != "restore" || versions[0].RestoredFrom == nil || *versions[0].RestoredFrom != 1 {
		t.Fatalf("after restore: %+v", versions)
	}

	if trash, err = f.r.ListDeletedContent(ctx, db.ListDeletedContentParams{
		WorkspaceID: f.workspaceID(), Lim: 10,
	}); err != nil || len(trash) != 0 {
		t.Fatalf("trash after restore = %+v, %v", trash, err)
	}
}

// TestContentDeleteMissingIsNotFound: the service maps a missing row to
// ErrNotFound before writing a delete version.
func TestContentDeleteMissingIsNotFound(t *testing.T) {
	t.Parallel()
	f := newContentFixture(t)
	ctx := context.Background()
	svc := content.New(f.r, f.r.TxContent)
	if err := svc.DeleteSavedQuery(ctx, f.actor(), f.workspaceID(), PGUUID(NewUUID())); !errors.Is(err, content.ErrNotFound) {
		t.Fatalf("delete missing = %v, want ErrNotFound", err)
	}
}

// TestContentSemanticMetricLifecycle covers the metric create/update/delete/
// restore path over GORM.
func TestContentSemanticMetricLifecycle(t *testing.T) {
	t.Parallel()
	f := newContentFixture(t)
	ctx := context.Background()
	svc := content.New(f.r, f.r.TxContent)

	m, err := svc.CreateSemanticMetric(ctx, f.actor(), db.CreateSemanticMetricParams{
		WorkspaceID: f.workspaceID(), SpaceID: f.spaceID(), Slug: "net_revenue", Label: "Net revenue",
		CatalogID: f.catalogID(), Namespace: []string{"sales"}, TableName: "orders",
		Definition: []byte(`{"type":"simple","aggregation":"count"}`), FormatVersion: 1,
		BindingID: f.bindingID(), UpdatedBy: f.actor(),
	})
	if err != nil {
		t.Fatalf("CreateSemanticMetric: %v", err)
	}
	if versions := f.versions(t, content.EntitySemanticMetric, m.ID); len(versions) != 1 || versions[0].ChangeKind != "create" {
		t.Fatalf("after create: %+v", versions)
	}

	update := db.UpdateSemanticMetricParams{
		WorkspaceID: f.workspaceID(), ID: m.ID, SpaceID: m.SpaceID, Slug: m.Slug, Label: "Net revenue",
		Description: "net", Certified: false, CatalogID: m.CatalogID, Namespace: m.Namespace,
		TableName: m.TableName, BindingID: m.BindingID, Definition: m.Definition,
		FormatVersion: 1, OwnerUserID: f.actor(), UpdatedBy: f.actor(),
	}
	out, changed, err := svc.UpdateSemanticMetric(ctx, f.actor(), update, nil)
	if err != nil || !changed || out.Description != "net" {
		t.Fatalf("update = %+v, changed=%v, err=%v", out, changed, err)
	}
	if _, changed, err = svc.UpdateSemanticMetric(ctx, f.actor(), update, nil); err != nil || changed {
		t.Fatalf("no-op update: changed=%v err=%v", changed, err)
	}
	if versions := f.versions(t, content.EntitySemanticMetric, m.ID); len(versions) != 2 {
		t.Fatalf("after update: %+v", versions)
	}

	if err := svc.DeleteSemanticMetric(ctx, f.actor(), f.workspaceID(), m.ID); err != nil {
		t.Fatalf("DeleteSemanticMetric: %v", err)
	}
	if _, err := f.r.GetSemanticMetric(ctx, db.GetSemanticMetricParams{ID: m.ID, WorkspaceID: f.workspaceID()}); !errors.Is(err, db.ErrNoRows) {
		t.Fatalf("deleted metric = %v, want db.ErrNoRows", err)
	}
	if versions := f.versions(t, content.EntitySemanticMetric, m.ID); len(versions) != 3 || versions[0].ChangeKind != "delete" {
		t.Fatalf("after delete: %+v", versions)
	}

	restored, err := svc.RestoreSemanticMetric(ctx, f.actor(), f.workspaceID(), m.ID, 1, nil, nil)
	if err != nil {
		t.Fatalf("RestoreSemanticMetric: %v", err)
	}
	if restored.ID != m.ID || restored.Slug != "net_revenue" {
		t.Fatalf("restored = %+v", restored)
	}
	if versions := f.versions(t, content.EntitySemanticMetric, m.ID); len(versions) != 4 || versions[0].ChangeKind != "restore" {
		t.Fatalf("after restore: %+v", versions)
	}
}

// TestContentSemanticModelLifecycle covers the model create/update/delete/
// restore path over GORM.
func TestContentSemanticModelLifecycle(t *testing.T) {
	t.Parallel()
	f := newContentFixture(t)
	ctx := context.Background()
	svc := content.New(f.r, f.r.TxContent)

	def := []byte(`{"source":{"kind":"table","namespace":["sales"],"table":"orders"},"primary_key":["id"],"dimensions":[],"metrics":[]}`)
	m, err := svc.CreateSemanticModel(ctx, f.actor(), db.CreateSemanticModelParams{
		WorkspaceID: f.workspaceID(), SpaceID: f.spaceID(), Slug: "orders_model", Label: "Orders",
		CatalogID: f.catalogID(), BindingID: f.bindingID(), Definition: def, FormatVersion: 1,
		OwnerUserID: f.actor(), UpdatedBy: f.actor(),
	})
	if err != nil {
		t.Fatalf("CreateSemanticModel: %v", err)
	}
	if versions := f.versions(t, content.EntitySemanticModel, m.ID); len(versions) != 1 || versions[0].ChangeKind != "create" {
		t.Fatalf("after create: %+v", versions)
	}

	update := db.UpdateSemanticModelParams{
		WorkspaceID: f.workspaceID(), ID: m.ID, SpaceID: m.SpaceID, Slug: m.Slug,
		Label: "Orders v2", Description: m.Description, Certified: m.Certified,
		CatalogID: m.CatalogID, BindingID: m.BindingID, Definition: m.Definition,
		FormatVersion: 1, OwnerUserID: m.OwnerUserID, UpdatedBy: f.actor(),
	}
	out, changed, err := svc.UpdateSemanticModel(ctx, f.actor(), update, nil)
	if err != nil || !changed || out.Label != "Orders v2" {
		t.Fatalf("update = %+v, changed=%v, err=%v", out, changed, err)
	}
	if _, changed, err = svc.UpdateSemanticModel(ctx, f.actor(), update, nil); err != nil || changed {
		t.Fatalf("no-op update: changed=%v err=%v", changed, err)
	}
	if versions := f.versions(t, content.EntitySemanticModel, m.ID); len(versions) != 2 {
		t.Fatalf("after update: %+v", versions)
	}

	if err := svc.DeleteSemanticModel(ctx, f.actor(), f.workspaceID(), m.ID); err != nil {
		t.Fatalf("DeleteSemanticModel: %v", err)
	}
	if _, err := f.r.GetSemanticModel(ctx, db.GetSemanticModelParams{ID: m.ID, WorkspaceID: f.workspaceID()}); !errors.Is(err, db.ErrNoRows) {
		t.Fatalf("deleted model = %v, want db.ErrNoRows", err)
	}
	if versions := f.versions(t, content.EntitySemanticModel, m.ID); len(versions) != 3 || versions[0].ChangeKind != "delete" {
		t.Fatalf("after delete: %+v", versions)
	}

	restored, err := svc.RestoreSemanticModel(ctx, f.actor(), f.workspaceID(), m.ID, 1, nil, nil)
	if err != nil {
		t.Fatalf("RestoreSemanticModel: %v", err)
	}
	if restored.ID != m.ID || restored.Label != "Orders" {
		t.Fatalf("restored = %+v", restored)
	}
	if versions := f.versions(t, content.EntitySemanticModel, m.ID); len(versions) != 4 || versions[0].ChangeKind != "restore" {
		t.Fatalf("after restore: %+v", versions)
	}
}

// TestContentBundleSources covers the catalog, binding and space reads the
// bundle exporter needs.
func TestContentBundleSources(t *testing.T) {
	t.Parallel()
	f := newContentFixture(t)
	ctx := context.Background()

	space, err := f.r.GetSpace(ctx, db.GetSpaceParams{WorkspaceID: f.workspaceID(), ID: f.spaceID()})
	if err != nil || space.ID != f.spaceID() {
		t.Fatalf("GetSpace = %+v, %v", space, err)
	}
	name, err := f.r.GetCatalogNameByID(ctx, f.catalogID())
	if err != nil || name != "lake" {
		t.Fatalf("GetCatalogNameByID = %q, %v", name, err)
	}
	granted, err := f.r.GetGrantedCatalogByName(ctx, db.GetGrantedCatalogByNameParams{
		WorkspaceID: f.workspaceID(), Name: "lake",
	})
	if err != nil || granted != f.catalogID() {
		t.Fatalf("GetGrantedCatalogByName = %v, %v", granted, err)
	}
	names, err := f.r.GetBindingTargetNames(ctx, f.bindingID())
	if err != nil || names.CatalogName != "lake" || names.EngineName != "trino" {
		t.Fatalf("GetBindingTargetNames = %+v, %v", names, err)
	}
	targets, err := f.r.ListWorkspaceTargets(ctx, f.workspaceID())
	if err != nil || len(targets) != 1 || targets[0].BindingID != f.bindingID() {
		t.Fatalf("ListWorkspaceTargets = %+v, %v", targets, err)
	}

	if _, err := f.r.GetCatalogNameByID(ctx, PGUUID(NewUUID())); !errors.Is(err, db.ErrNoRows) {
		t.Fatalf("missing catalog = %v, want db.ErrNoRows", err)
	}
	if _, err := f.r.GetGrantedCatalogByName(ctx, db.GetGrantedCatalogByNameParams{
		WorkspaceID: f.workspaceID(), Name: "absent",
	}); !errors.Is(err, db.ErrNoRows) {
		t.Fatalf("missing grant = %v, want db.ErrNoRows", err)
	}
}
