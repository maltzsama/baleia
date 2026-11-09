package api

import (
	"context"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/config"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/store/model"
	"github.com/galileostudio/baleia/internal/store/repo"
	"github.com/galileostudio/baleia/internal/temporal/schedules"
	"github.com/galileostudio/baleia/internal/testdb"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.temporal.io/sdk/client"
	"gorm.io/gorm"
)

type attentionFixture struct {
	pool      *pgxpool.Pool
	gdb       *gorm.DB
	h         *API
	admin     context.Context
	owner     context.Context
	stranger  context.Context
	ownerID   string
	catalogID pgtype.UUID
	engineID  pgtype.UUID
	bindingID pgtype.UUID
	wsID      pgtype.UUID
	cache     *TTLStore[[]AttentionItem]
}

func newAttentionFixture(t *testing.T) *attentionFixture {
	t.Helper()
	pool := testdb.New(t)
	ctx := context.Background()
	gdb := testdb.GORM(t, pool)
	q := repo.New(gdb)
	f := &attentionFixture{pool: pool, gdb: gdb}

	user := func(email, role string) pgtype.UUID {
		password := "x"
		row := model.User{
			ID: model.UUID(repo.NewUUID()), Name: email, Email: model.Text(email),
			Role: model.Text(role), PasswordHash: &password,
		}
		if err := gdb.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
		return repo.PGUUID(string(row.ID))
	}
	f.ownerID = idString(user("owner@attention.test", "member"))
	strangerID := user("stranger@attention.test", "member")
	adminID := user("admin@attention.test", "admin")
	f.admin = ctxAs(idString(adminID), "admin")
	f.owner = ctxAs(f.ownerID, "member")
	f.stranger = ctxAs(idString(strangerID), "member")

	conn := func(name, owner string) pgtype.UUID {
		row, err := q.CreateCatalogConnection(ctx, db.CreateCatalogConnectionParams{
			ID: repo.PGUUID(repo.NewUUID()), Name: name, Type: "polaris",
			Url: "http://polaris.test", CreatedBy: repo.PGUUID(owner),
		})
		if err != nil {
			t.Fatal(err)
		}
		return row.ID
	}
	catConn := conn("attention-source", f.ownerID)
	engConn := conn("attention-engine", f.ownerID)

	var cluster model.TrinoCluster
	if err := gdb.Order("name").First(&cluster).Error; err != nil {
		t.Fatal(err)
	}
	trinoCatalog := model.Text("attention_catalog")
	warehouse := "wh"
	connector := model.Text("iceberg")
	clusterID := cluster.ID
	catalog := model.Catalog{
		ID: model.UUID(repo.NewUUID()), ConnectionID: model.UUID(repo.UUIDString(catConn)),
		Name: "attention_catalog", TrinoCatalogName: &trinoCatalog, ClusterID: &clusterID,
		Warehouse: &warehouse, Connector: &connector, Origin: "source",
	}
	if err := gdb.Create(&catalog).Error; err != nil {
		t.Fatal(err)
	}
	f.catalogID = repo.PGUUID(string(catalog.ID))

	engine, err := q.CreateEngine(ctx, db.CreateEngineParams{
		Name: "attention_engine", Kind: "trino", Class: "federated", ConnectionID: engConn,
		IdentityMode: "connection", CreatedBy: repo.PGUUID(f.ownerID),
	})
	if err != nil {
		t.Fatal(err)
	}
	f.engineID = engine.ID
	binding := model.CatalogBinding{
		ID: model.UUID(repo.NewUUID()), CatalogID: catalog.ID,
		EngineID: model.UUID(repo.UUIDString(f.engineID)), EngineCatalogName: "attention_catalog",
		Mode: "managed", Enabled: true, LifecycleState: "error",
		StateChangedAt: time.Now().Add(-time.Hour),
	}
	if err := gdb.Create(&binding).Error; err != nil {
		t.Fatal(err)
	}
	f.bindingID = repo.PGUUID(string(binding.ID))
	owner := model.UUID(f.ownerID)
	ws := model.Workspace{
		ID: model.UUID(repo.NewUUID()), Name: "attention-ws", Type: "personal",
		PolarisCatalogName: "attention_polaris", OwnerUserID: &owner,
	}
	if err := gdb.Create(&ws).Error; err != nil {
		t.Fatal(err)
	}
	f.wsID = repo.PGUUID(string(ws.ID))
	addDefaultSpace(t, gdb, ws.ID)

	f.cache = NewTTLStore[[]AttentionItem](time.Hour)
	f.h = testAPI(handlerFields{
		queries:        q,
		pool:           pool,
		tx:             poolTx{pool: pool},
		settings:       SettingsFromConfig(&config.Config{AuditExportURL: "https://opensearch.test"}),
		attentionCache: f.cache,
	})
	return f
}

func (f *attentionFixture) list(t *testing.T, ctx context.Context) []AttentionItem {
	t.Helper()
	got, err := f.h.ListAttention(ctx, ListAttentionRequestObject{})
	if err != nil {
		t.Fatal(err)
	}
	resp, ok := got.(ListAttention200JSONResponse)
	if !ok {
		t.Fatalf("list attention = %T", got)
	}
	return resp
}

func hasKind(items []AttentionItem, kind AttentionKind) bool {
	for _, it := range items {
		if it.Kind == kind {
			return true
		}
	}
	return false
}

// TestAttention_Scoped: the owner of a broken binding sees it; another member
// with no access does not.
func TestAttention_Scoped(t *testing.T) {
	t.Parallel()
	f := newAttentionFixture(t)

	items := f.list(t, f.owner)
	if !hasKind(items, BindingError) {
		t.Fatalf("owner items = %+v, want binding_error", items)
	}
	found := false
	for _, it := range items {
		if it.Kind != BindingError {
			continue
		}
		found = true
		if it.Resource.Id != idString(f.bindingID) || it.Resource.Type != AttentionResourceTypeBinding {
			t.Errorf("binding resource = %+v", it.Resource)
		}
		if it.Severity != AttentionSeverityError || it.Href == nil {
			t.Errorf("binding item = %+v", it)
		}
	}
	if !found {
		t.Fatal("binding_error not found")
	}

	if got := f.list(t, f.stranger); hasKind(got, BindingError) {
		t.Errorf("stranger sees another user's binding error: %+v", got)
	}
}

// TestAttention_AdminOnlyKinds: drift and export lag are admin-only.
func TestAttention_AdminOnlyKinds(t *testing.T) {
	t.Parallel()
	f := newAttentionFixture(t)

	now := time.Now()
	actions := model.JSON(`[{"catalog":"c","kind":"create"}]`)
	if err := f.gdb.Create(&model.CatalogReconcileRun{
		ID: model.UUID(repo.NewUUID()), EngineID: model.UUID(repo.UUIDString(f.engineID)),
		StartedAt: now.Add(-5 * time.Minute), FinishedAt: now, DryRun: true, Actions: actions,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.gdb.Create(&model.AuditEvent{
		OccurredAt: now.Add(-30 * time.Minute), ActorKind: "user", Action: "catalog.update",
		ResourceType: "catalog", Outcome: "allowed",
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.gdb.Create(&model.AuditExportCursor{
		Sink: "opensearch", OccurredAt: now.Add(-time.Hour), ID: 0,
	}).Error; err != nil {
		t.Fatal(err)
	}

	member := f.list(t, f.owner)
	if hasKind(member, ReconcileDrift) || hasKind(member, AuditExportLagging) || hasKind(member, ScheduleMissing) {
		t.Errorf("member sees admin-only kinds: %+v", member)
	}

	admin := f.list(t, f.admin)
	if !hasKind(admin, ReconcileDrift) {
		t.Errorf("admin does not see reconcile_drift: %+v", admin)
	}
	if !hasKind(admin, AuditExportLagging) {
		t.Errorf("admin does not see audit_export_lagging: %+v", admin)
	}
}

// TestAttention_Cache: the endpoint returns the 30 s cache and recomputes
// after it is cleared.
func TestAttention_Cache(t *testing.T) {
	t.Parallel()
	f := newAttentionFixture(t)

	if !hasKind(f.list(t, f.owner), BindingError) {
		t.Fatal("binding_error not found")
	}

	// Fix it behind the cache's back.
	if err := f.gdb.Model(&model.CatalogBinding{}).
		Where("id = ?", repo.UUIDString(f.bindingID)).
		Update("lifecycle_state", "active").Error; err != nil {
		t.Fatal(err)
	}
	if !hasKind(f.list(t, f.owner), BindingError) {
		t.Fatal("the cached response changed before it expired")
	}

	// Force a recompute.
	f.cache.Clear()
	if hasKind(f.list(t, f.owner), BindingError) {
		t.Fatal("binding_error survived the cache clear")
	}
}

// TestAttention_ClearsWhenFixed: an item disappears when its condition clears.
func TestAttention_ClearsWhenFixed(t *testing.T) {
	t.Parallel()
	f := newAttentionFixture(t)

	now := time.Now()
	if err := f.gdb.Create(&model.MetadataSyncRun{
		ID: model.UUID(repo.NewUUID()), CatalogID: model.UUID(repo.UUIDString(f.catalogID)),
		Status: "failed", SourcePath: "provider", ErrorCode: ptrTo("engine_unreachable"),
		FinishedAt: ptrTo(now.Add(-2 * time.Hour)),
	}).Error; err != nil {
		t.Fatal(err)
	}
	if !hasKind(f.list(t, f.owner), MetadataSyncFailed) {
		t.Fatalf("metadata_sync_failed not found")
	}

	// A newer successful pass clears it.
	if err := f.gdb.Create(&model.MetadataSyncRun{
		ID: model.UUID(repo.NewUUID()), CatalogID: model.UUID(repo.UUIDString(f.catalogID)),
		Status: "finished", SourcePath: "provider", FinishedAt: &now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	f.cache.Clear()
	if hasKind(f.list(t, f.owner), MetadataSyncFailed) {
		t.Fatal("metadata_sync_failed survived a successful pass")
	}
}

// TestAttention_LineageBroken: removing a column a model reads raises the
// lineage_broken item for the workspace owner, and no one else (B4, #558).
func TestAttention_LineageBroken(t *testing.T) {
	t.Parallel()
	f := newAttentionFixture(t)
	ctx := context.Background()

	table := model.MetadataTable{
		ID: model.UUID(repo.NewUUID()), CatalogID: model.UUID(repo.UUIDString(f.catalogID)),
		Namespace: model.StringArray{"sales"}, Name: "orders", Kind: "table", Fingerprint: "f",
		LastSeenAt: time.Now(), LastChangedAt: time.Now(),
	}
	if err := f.gdb.Create(&table).Error; err != nil {
		t.Fatal(err)
	}
	tableID := repo.PGUUID(string(table.ID))
	nullable := func(v bool) *bool { return &v }
	for _, col := range []struct {
		ordinal  int32
		name     model.Text
		typ      string
		nullable *bool
	}{{1, "id", "long", nullable(false)}, {2, "note", "string", nullable(true)}} {
		if err := f.gdb.Create(&model.MetadataColumn{
			TableID: table.ID, Ordinal: col.ordinal, Name: col.name,
			NativeType: col.typ, TypeSystem: "iceberg", Nullable: col.nullable,
		}).Error; err != nil {
			t.Fatal(err)
		}
	}

	space, err := f.h.queries.GetDefaultSpace(ctx, f.wsID)
	if err != nil {
		t.Fatal(err)
	}
	owner := pgtype.UUID{Bytes: uuid.MustParse(f.ownerID), Valid: true}
	def := []byte(`{"source":{"kind":"table","namespace":["sales"],"table":"orders"},
		"primary_key":["id"],
		"dimensions":[{"name":"id","type":"int64"},{"name":"note","sql":"note","type":"string"}]}`)
	if _, err := f.h.queries.CreateSemanticModel(ctx, db.CreateSemanticModelParams{
		WorkspaceID: f.wsID, SpaceID: space.ID, Slug: "broken_note", Label: "Broken",
		CatalogID: f.catalogID, BindingID: f.bindingID, Definition: def, FormatVersion: 1,
		OwnerUserID: owner, UpdatedBy: owner,
	}); err != nil {
		t.Fatal(err)
	}

	if hasKind(f.list(t, f.owner), LineageBroken) {
		t.Fatal("lineage_broken raised before any column was removed")
	}

	if err := f.gdb.Where("table_id = ? AND name = ?", repo.UUIDString(tableID), "note").
		Delete(&model.MetadataColumn{}).Error; err != nil {
		t.Fatal(err)
	}
	f.cache.Clear()
	items := f.list(t, f.owner)
	if !hasKind(items, LineageBroken) {
		t.Fatalf("items = %+v, want lineage_broken", items)
	}
	for _, it := range items {
		if it.Kind != LineageBroken {
			continue
		}
		if it.Resource.Type != AttentionResourceTypeModel || it.Href == nil {
			t.Errorf("lineage_broken item = %+v", it)
		}
	}

	if hasKind(f.list(t, f.stranger), LineageBroken) {
		t.Fatal("stranger sees another workspace's lineage_broken")
	}
}

func TestScheduleStaleItem(t *testing.T) {
	t.Parallel()
	old := time.Now().Add(-4 * time.Hour)
	recent := time.Now()
	cases := []struct {
		name string
		id   string
		desc *client.ScheduleDescription
		want bool
	}{
		{"stale table-health", schedules.LensTableHealth, &client.ScheduleDescription{Info: client.ScheduleInfo{RecentActions: []client.ScheduleActionResult{{ActualTime: old}}}}, true},
		{"fresh table-health", schedules.LensTableHealth, &client.ScheduleDescription{Info: client.ScheduleInfo{RecentActions: []client.ScheduleActionResult{{ActualTime: recent}}}}, false},
		{"unknown cadence", "other-schedule", &client.ScheduleDescription{Info: client.ScheduleInfo{RecentActions: []client.ScheduleActionResult{{ActualTime: old}}}}, false},
		{"no actions", schedules.LensTableHealth, &client.ScheduleDescription{}, false},
		{"no actions, old schedule", schedules.LensTableHealth, &client.ScheduleDescription{Info: client.ScheduleInfo{CreatedAt: old}}, true},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := scheduleStaleItem(c.id, c.desc); c.want != (got != nil) {
				t.Fatalf("scheduleStaleItem(%q) = %v, want nil=%v", c.id, got, !c.want)
			}
		})
	}
}
