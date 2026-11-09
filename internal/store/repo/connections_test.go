package repo

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/store/model"
	"github.com/galileostudio/baleia/internal/testdb"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/stdlib"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

func strPtr(s string) *string { return &s }

func int64Ptr(v int64) *int64 { return &v }

func containsConnection(rows []db.CatalogConnection, id pgtype.UUID) bool {
	for _, row := range rows {
		if row.ID == id {
			return true
		}
	}
	return false
}

func containsEngine(rows []db.Engine, id pgtype.UUID) bool {
	for _, row := range rows {
		if row.ID == id {
			return true
		}
	}
	return false
}

// TestConnectionsAndCatalogsRoundTrip (#771) exercises the GORM persistence of
// connections, engines, catalogs, bindings, queue capacities, grants and the
// registry, which the topology service and the catalog handlers run on.
func TestConnectionsAndCatalogsRoundTrip(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	sqlDB := stdlib.OpenDBFromPool(pool)
	gdb, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{
		Logger:         gormlogger.Discard,
		TranslateError: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	ctx := context.Background()
	r := New(gdb)
	now := time.Now().UTC()

	owner := createAPIKeyUser(t, gdb, "topology-owner@example.test")
	outsider := createAPIKeyUser(t, gdb, "topology-outsider@example.test")
	ownerID := PGUUID(string(owner.ID))
	outsiderID := PGUUID(string(outsider.ID))

	team, err := NewTeams(gdb).CreateTeam(ctx, db.CreateTeamParams{Name: "topology", CreatedBy: ownerID})
	if err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	ws, err := r.CreateWorkspace(ctx, db.CreateWorkspaceParams{
		Name: "Topology", Type: "shared", PolarisCatalogName: "ws_topology_test", OwnerTeamID: team.ID,
	})
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}

	// ── connections ────────────────────────────────────────────────────────
	connID := pgtype.UUID{Bytes: uuid.New(), Valid: true}
	trino := "trino"
	conn, err := r.CreateCatalogConnection(ctx, db.CreateCatalogConnectionParams{
		ID: connID, Name: "polaris-prod", Type: "polaris", AccessMode: &trino, Url: "http://polaris.test",
		Config: []byte(`{"realm":"prod"}`), CredentialsEnc: strPtr("sealed"),
		CredentialsReadOnly: true, CreatedBy: ownerID,
	})
	if err != nil {
		t.Fatalf("CreateCatalogConnection: %v", err)
	}
	if conn.Name != "polaris-prod" || conn.CreatedBy != ownerID || !conn.CredentialsReadOnly || !conn.CreatedAt.Valid {
		t.Fatalf("CreateCatalogConnection = %+v", conn)
	}
	if conn.AccessMode == nil || *conn.AccessMode != "trino" {
		t.Fatalf("access_mode = %v, want trino", conn.AccessMode)
	}

	got, err := r.GetCatalogConnectionByID(ctx, connID)
	if err != nil || got.Name != conn.Name || got.Url != conn.Url {
		t.Fatalf("GetCatalogConnectionByID = %+v, %v", got, err)
	}
	if exists, err := r.CatalogConnectionNameExists(ctx, "polaris-prod"); err != nil || !exists {
		t.Errorf("CatalogConnectionNameExists = %v, %v", exists, err)
	}
	if n, err := r.CountEncryptedConnections(ctx); err != nil || n != 1 {
		t.Errorf("CountEncryptedConnections = %d, %v", n, err)
	}
	if own, err := r.ListCatalogConnections(ctx, ownerID); err != nil || len(own) != 1 {
		t.Errorf("ListCatalogConnections = %d, %v", len(own), err)
	}
	if rows, err := r.ListVisibleCatalogConnections(ctx, db.ListVisibleCatalogConnectionsParams{UserID: outsiderID}); err != nil || len(rows) != 0 {
		t.Errorf("outsider connections = %d, %v", len(rows), err)
	}
	if rows, err := r.ListVisibleCatalogConnections(ctx, db.ListVisibleCatalogConnectionsParams{IsAdmin: true, UserID: outsiderID}); err != nil || !containsConnection(rows, connID) {
		t.Errorf("admin connections = %d, %v", len(rows), err)
	}

	renamed := "polaris-prod-renamed"
	ro := false
	updated, err := r.UpdateCatalogConnection(ctx, db.UpdateCatalogConnectionParams{
		ID: connID, Name: &renamed, CredentialsReadOnly: &ro,
	})
	if err != nil {
		t.Fatalf("UpdateCatalogConnection: %v", err)
	}
	if updated.Name != renamed || updated.Url != "http://polaris.test" || updated.CredentialsReadOnly {
		t.Fatalf("partial update = %+v", updated)
	}

	// ── engines, clusters and observation ─────────────────────────────────
	engine, err := r.CreateEngine(ctx, db.CreateEngineParams{
		Name: "trino-main", Kind: "trino", Class: "federated", ConnectionID: connID,
		IdentityMode: "connection", CreatedBy: ownerID,
	})
	if err != nil {
		t.Fatalf("CreateEngine: %v", err)
	}
	if !engine.Enabled || engine.ConnectionID != connID {
		t.Fatalf("CreateEngine = %+v", engine)
	}
	if engines, err := r.ListEngines(ctx); err != nil || !containsEngine(engines, engine.ID) {
		t.Fatalf("ListEngines = %d, %v", len(engines), err)
	}
	if engines, err := r.ListEnginesVisibleToUser(ctx, ownerID); err != nil || len(engines) != 1 {
		t.Errorf("ListEnginesVisibleToUser = %d, %v", len(engines), err)
	}
	if engines, err := r.ListEnginesVisibleToUser(ctx, outsiderID); err != nil || len(engines) != 0 {
		t.Errorf("outsider engines = %d, %v", len(engines), err)
	}

	disabled := false
	costRouting := true
	threshold := int64(1024)
	if _, err := r.UpdateEngine(ctx, db.UpdateEngineParams{
		ID: engine.ID, Enabled: &disabled, CostRoutingEnabled: &costRouting, CostThresholdBytes: &threshold,
	}); err != nil {
		t.Fatalf("UpdateEngine: %v", err)
	}
	clearThreshold := true
	after, err := r.UpdateEngine(ctx, db.UpdateEngineParams{ID: engine.ID, ClearCostThreshold: &clearThreshold})
	if err != nil || after.Enabled || !after.CostRoutingEnabled || after.CostThresholdBytes != nil {
		t.Fatalf("cleared engine = %+v, %v", after, err)
	}

	cluster := model.TrinoCluster{
		ID: model.UUID(NewUUID()), Name: model.Text("topology-test-cluster"),
		CoordinatorURL: "http://trino.test", CreatedAt: now,
	}
	if err := gdb.Create(&cluster).Error; err != nil {
		t.Fatalf("create cluster: %v", err)
	}
	if err := r.LinkTrinoClusterEngine(ctx, db.LinkTrinoClusterEngineParams{ID: PGUUID(string(cluster.ID)), EngineID: engine.ID}); err != nil {
		t.Fatalf("LinkTrinoClusterEngine: %v", err)
	}
	if def, err := r.GetDefaultTrinoCluster(ctx); err != nil || !def.ID.Valid {
		t.Fatalf("GetDefaultTrinoCluster = %+v, %v", def, err)
	}
	if byEngine, err := r.GetTrinoClusterByEngine(ctx, engine.ID); err != nil || byEngine.ID != PGUUID(string(cluster.ID)) {
		t.Errorf("GetTrinoClusterByEngine = %+v, %v", byEngine, err)
	}

	// ── queue capacities ───────────────────────────────────────────────────
	for _, c := range []db.UpsertEngineQueueCapacityParams{
		{EngineID: engine.ID, Class: "interactive", MaxRunning: 4, MaxQueued: 16, QueueTimeoutMs: 1000, RuntimeTimeoutMs: int64Ptr(5000)},
		{EngineID: engine.ID, Class: "heavy", MaxRunning: 2, MaxQueued: 8, QueueTimeoutMs: 2000},
	} {
		if err := r.UpsertEngineQueueCapacity(ctx, c); err != nil {
			t.Fatalf("UpsertEngineQueueCapacity(%s): %v", c.Class, err)
		}
	}
	caps, err := r.ListEngineQueueCapacities(ctx, engine.ID)
	if err != nil || len(caps) != 2 || caps[0].Class != "heavy" || caps[1].Class != "interactive" {
		t.Fatalf("ListEngineQueueCapacities = %+v, %v", caps, err)
	}
	if caps[1].RuntimeTimeoutMs == nil || *caps[1].RuntimeTimeoutMs != 5000 {
		t.Errorf("runtime timeout = %+v", caps[1].RuntimeTimeoutMs)
	}
	if err := r.DeleteEngineQueueCapacities(ctx, engine.ID); err != nil {
		t.Fatalf("DeleteEngineQueueCapacities: %v", err)
	}
	if caps, err := r.ListEngineQueueCapacities(ctx, engine.ID); err != nil || len(caps) != 0 {
		t.Errorf("capacities after delete = %d, %v", len(caps), err)
	}

	if err := r.UpsertEngineCatalogObservation(ctx, db.UpsertEngineCatalogObservationParams{
		EngineID: engine.ID, CatalogName: "lake", Mode: "managed", Connector: strPtr("iceberg"),
	}); err != nil {
		t.Fatalf("UpsertEngineCatalogObservation: %v", err)
	}
	obs, err := r.ListEngineObservations(ctx, engine.ID)
	if err != nil || len(obs) != 1 || !obs[0].Present || obs[0].Mode != "managed" {
		t.Fatalf("ListEngineObservations = %+v, %v", obs, err)
	}
	if err := r.MarkEngineCatalogsAbsent(ctx, db.MarkEngineCatalogsAbsentParams{EngineID: engine.ID}); err != nil {
		t.Fatalf("MarkEngineCatalogsAbsent: %v", err)
	}
	obs, err = r.ListEngineObservations(ctx, engine.ID)
	if err != nil || len(obs) != 1 || obs[0].Present {
		t.Errorf("absent observation = %+v, %v", obs, err)
	}
	if err := r.MarkEngineCatalogManaged(ctx, db.MarkEngineCatalogManagedParams{EngineID: engine.ID, CatalogName: "lake"}); err != nil {
		t.Fatalf("MarkEngineCatalogManaged: %v", err)
	}
	if names, err := r.ObservedManagedNamesOfEngine(ctx, engine.ID); err != nil || len(names) != 1 || names[0] != "lake" {
		t.Errorf("ObservedManagedNamesOfEngine = %v, %v", names, err)
	}

	// ── catalogs ───────────────────────────────────────────────────────────
	catalog, err := r.CreateCatalog(ctx, db.CreateCatalogParams{
		ConnectionID: connID, Name: "lakehouse", Description: strPtr("main"),
		TrinoCatalogName: strPtr("lake_main"), ClusterID: PGUUID(string(cluster.ID)),
		Warehouse: strPtr("s3://lake"), Connector: strPtr("iceberg"), ExtraProps: []byte(`{"x":"y"}`),
	})
	if err != nil {
		t.Fatalf("CreateCatalog: %v", err)
	}
	if catalog.SourceRef == nil || *catalog.SourceRef != "s3://lake" || catalog.Origin != "source" {
		t.Fatalf("CreateCatalog = %+v", catalog)
	}
	if got, err := r.GetCatalogByID(ctx, catalog.ID); err != nil || got.Name != "lakehouse" {
		t.Fatalf("GetCatalogByID = %+v, %v", got, err)
	}
	if got, err := r.GetCatalogByTrinoName(ctx, db.GetCatalogByTrinoNameParams{
		ClusterID: PGUUID(string(cluster.ID)), TrinoCatalogName: "lake_main",
	}); err != nil || got.ID != catalog.ID {
		t.Errorf("GetCatalogByTrinoName = %+v, %v", got, err)
	}
	if rows, err := r.ListCatalogsByConnection(ctx, connID); err != nil || len(rows) != 1 {
		t.Errorf("ListCatalogsByConnection = %d, %v", len(rows), err)
	}
	if rows, err := r.ListVisibleCatalogs(ctx, db.ListVisibleCatalogsParams{IsAdmin: true}); err != nil || len(rows) != 1 {
		t.Errorf("admin catalogs = %d, %v", len(rows), err)
	}
	if rows, err := r.ListVisibleCatalogs(ctx, db.ListVisibleCatalogsParams{UserID: ownerID}); err != nil || len(rows) != 1 {
		t.Errorf("owner catalogs = %d, %v", len(rows), err)
	}
	if rows, err := r.ListVisibleCatalogs(ctx, db.ListVisibleCatalogsParams{UserID: outsiderID}); err != nil || len(rows) != 0 {
		t.Errorf("outsider catalogs = %d, %v", len(rows), err)
	}

	next := "lakehouse-v2"
	trinoNext := "lake_main_v2"
	warehouseNext := "s3://lake2"
	if got, err := r.UpdateCatalog(ctx, db.UpdateCatalogParams{
		ID: catalog.ID, Name: next, Description: strPtr("v2"), Warehouse: &warehouseNext,
		Connector: strPtr("iceberg"), ExtraProps: []byte(`{}`), TrinoCatalogName: &trinoNext,
	}); err != nil || got.Name != next || got.SourceRef == nil || *got.SourceRef != warehouseNext {
		t.Fatalf("UpdateCatalog = %+v, %v", got, err)
	}
	if err := r.SetCatalogEnabled(ctx, db.SetCatalogEnabledParams{ID: catalog.ID, Enabled: false}); err != nil {
		t.Fatalf("SetCatalogEnabled(false): %v", err)
	}
	if got, err := r.GetCatalogByID(ctx, catalog.ID); err != nil || got.Enabled {
		t.Fatalf("disabled catalog = %+v, %v", got, err)
	}
	if err := r.SetCatalogEnabled(ctx, db.SetCatalogEnabledParams{ID: catalog.ID, Enabled: true}); err != nil {
		t.Fatalf("SetCatalogEnabled(true): %v", err)
	}
	if err := r.SetCatalogDiscoverable(ctx, db.SetCatalogDiscoverableParams{ID: catalog.ID, Discoverable: true}); err != nil {
		t.Fatalf("SetCatalogDiscoverable: %v", err)
	}
	tableID := model.UUID(NewUUID())
	if err := gdb.Create(&model.MetadataTable{
		ID: tableID, CatalogID: model.UUID(UUIDString(catalog.ID)), Namespace: model.StringArray{"sales"},
		Name: model.Text("orders"), Kind: "table", Fingerprint: "f",
		LastSeenAt: now, LastChangedAt: now,
	}).Error; err != nil {
		t.Fatalf("create metadata table: %v", err)
	}
	if got, err := r.GetMetadataTableByName(ctx, db.GetMetadataTableByNameParams{
		CatalogID: catalog.ID, Namespace: []string{"sales"}, Name: "orders",
	}); err != nil || got != PGUUID(string(tableID)) {
		t.Fatalf("GetMetadataTableByName = %v, %v", got, err)
	}
	if cols, err := r.ListMetadataColumns(ctx, PGUUID(string(tableID))); err != nil || len(cols) != 0 {
		t.Errorf("ListMetadataColumns = %d, %v", len(cols), err)
	}

	discoverable, err := r.ListDiscoverableCatalogs(ctx)
	if err != nil || len(discoverable) != 1 || discoverable[0].EngineCatalogName != trinoNext || discoverable[0].ConnectionName != renamed {
		t.Fatalf("ListDiscoverableCatalogs = %+v, %v", discoverable, err)
	}

	// ── bindings ───────────────────────────────────────────────────────────
	binding, err := r.UpsertManagedBinding(ctx, db.UpsertManagedBindingParams{
		CatalogID: catalog.ID, EngineID: engine.ID, EngineCatalogName: "lake_main",
		Connector: strPtr("iceberg"), ExtraProps: []byte(`{"a":"b"}`), Enabled: true,
		LifecycleState: "provisioning", DesiredVersion: strPtr("v1"),
	})
	if err != nil {
		t.Fatalf("UpsertManagedBinding: %v", err)
	}
	if binding.Mode != "managed" || binding.LifecycleState != "provisioning" {
		t.Fatalf("UpsertManagedBinding = %+v", binding)
	}
	// The same pair updates the binding in place instead of inserting.
	again, err := r.UpsertManagedBinding(ctx, db.UpsertManagedBindingParams{
		CatalogID: catalog.ID, EngineID: engine.ID, EngineCatalogName: "lake_main",
		Connector: strPtr("iceberg"), ExtraProps: []byte(`{}`), Enabled: true,
		LifecycleState: "active", DesiredVersion: strPtr("v2"),
	})
	if err != nil || again.ID != binding.ID || again.LifecycleState != "active" {
		t.Fatalf("re-upsert binding = %+v, %v", again, err)
	}
	if got, err := r.GetBinding(ctx, binding.ID); err != nil || got.DesiredVersion == nil || *got.DesiredVersion != "v2" {
		t.Fatalf("GetBinding = %+v, %v", got, err)
	}
	if got, err := r.GetManagedBindingOfCatalog(ctx, db.GetManagedBindingOfCatalogParams{CatalogID: catalog.ID, EngineID: engine.ID}); err != nil || got.ID != binding.ID {
		t.Errorf("GetManagedBindingOfCatalog = %+v, %v", got, err)
	}
	if rows, err := r.ListBindingsByEngine(ctx, engine.ID); err != nil || len(rows) != 1 {
		t.Errorf("ListBindingsByEngine = %d, %v", len(rows), err)
	}
	if rows, err := r.ListBindingsByCatalog(ctx, catalog.ID); err != nil || len(rows) != 1 {
		t.Errorf("ListBindingsByCatalog = %d, %v", len(rows), err)
	}
	if rows, err := r.ListManagedBindingsOfEngine(ctx, engine.ID); err != nil || len(rows) != 1 {
		t.Errorf("ListManagedBindingsOfEngine = %d, %v", len(rows), err)
	}

	// A guarded update from the wrong state is a concurrent change.
	if _, err := r.UpdateBinding(ctx, db.UpdateBindingParams{
		ID: binding.ID, EngineCatalogName: "lake_main", Enabled: true,
		LifecycleState: "updating", FromStates: []string{"error"},
	}); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("update from the wrong state = %v, want pgx.ErrNoRows", err)
	}
	updatedBinding, err := r.UpdateBinding(ctx, db.UpdateBindingParams{
		ID: binding.ID, EngineCatalogName: "lake_main", Connector: strPtr("iceberg"),
		ExtraProps: []byte(`{}`), Enabled: true, LifecycleState: "updating",
		DesiredVersion: strPtr("v3"), FromStates: []string{"active"},
	})
	if err != nil || updatedBinding.LifecycleState != "updating" {
		t.Fatalf("UpdateBinding = %+v, %v", updatedBinding, err)
	}
	transitioned, err := r.TransitionBinding(ctx, db.TransitionBindingParams{
		ID: binding.ID, ToState: "active", EngineCatalogName: strPtr("lake_main"),
		DesiredVersion: strPtr("v4"), FromStates: []string{"updating"},
	})
	if err != nil || transitioned.LifecycleState != "active" || *transitioned.DesiredVersion != "v4" {
		t.Fatalf("TransitionBinding = %+v, %v", transitioned, err)
	}

	// The registry row decides the derived binding status.
	if err := r.UpsertTrinoCatalogRegistry(ctx, db.UpsertTrinoCatalogRegistryParams{
		ClusterID: PGUUID(string(cluster.ID)), CatalogName: "lake_main",
		ConnectorName: "iceberg", Column4: []byte(`{"x":"y"}`), CatalogVersion: strPtr("v4"),
	}); err != nil {
		t.Fatalf("UpsertTrinoCatalogRegistry: %v", err)
	}
	state, err := r.GetTrinoCatalogRegistryState(ctx, db.GetTrinoCatalogRegistryStateParams{
		ClusterID: PGUUID(string(cluster.ID)), CatalogName: "lake_main",
	})
	if err != nil || !state.Enabled || state.SyncStatus != "pending" || state.UpdatedBy != "baleia" {
		t.Fatalf("GetTrinoCatalogRegistryState = %+v, %v", state, err)
	}
	status, err := r.GetBindingStatus(ctx, binding.ID)
	if err != nil || status.BindingID != binding.ID || status.SyncStatus != "pending" {
		t.Fatalf("GetBindingStatus = %+v, %v", status, err)
	}
	if err := r.SoftDeleteTrinoCatalogRegistry(ctx, db.SoftDeleteTrinoCatalogRegistryParams{
		ClusterID: PGUUID(string(cluster.ID)), CatalogName: "lake_main",
	}); err != nil {
		t.Fatalf("SoftDeleteTrinoCatalogRegistry: %v", err)
	}

	// Ephemeral registry rows expire and are swept.
	if err := r.InsertEphemeralRegistryRow(ctx, db.InsertEphemeralRegistryRowParams{
		ClusterID: PGUUID(string(cluster.ID)), CatalogName: "baleia_test_1", ConnectorName: "iceberg",
		Column4: []byte(`{}`), CatalogVersion: strPtr("v1"),
		Ttl: pgtype.Interval{Microseconds: -1, Valid: true},
	}); err != nil {
		t.Fatalf("InsertEphemeralRegistryRow: %v", err)
	}
	expired, err := r.ListExpiredEphemeralRegistryRows(ctx)
	if err != nil || len(expired) != 1 || expired[0].CatalogName != "baleia_test_1" {
		t.Fatalf("ListExpiredEphemeralRegistryRows = %+v, %v", expired, err)
	}
	if err := r.DeleteEphemeralRegistryRow(ctx, db.DeleteEphemeralRegistryRowParams{
		ClusterID: PGUUID(string(cluster.ID)), CatalogName: "baleia_test_1",
	}); err != nil {
		t.Fatalf("DeleteEphemeralRegistryRow: %v", err)
	}
	if expired, err := r.ListExpiredEphemeralRegistryRows(ctx); err != nil || len(expired) != 0 {
		t.Errorf("expired after delete = %d, %v", len(expired), err)
	}

	// A native catalog of the engine is stored, disabled when the engine stops
	// reporting it, and deleted with the engine.
	native, err := r.UpsertNativeCatalog(ctx, db.UpsertNativeCatalogParams{ConnectionID: connID, Name: "native_lake"})
	if err != nil || native.Origin != "native" {
		t.Fatalf("UpsertNativeCatalog = %+v, %v", native, err)
	}
	nativeBinding, err := r.UpsertNativeBinding(ctx, db.UpsertNativeBindingParams{
		CatalogID: native.ID, EngineID: engine.ID, EngineCatalogName: "native_lake",
	})
	if err != nil || nativeBinding.Mode != "native" || !nativeBinding.Enabled {
		t.Fatalf("UpsertNativeBinding = %+v, %v", nativeBinding, err)
	}
	if n, err := r.DisableMissingNativeBindings(ctx, db.DisableMissingNativeBindingsParams{
		EngineID: engine.ID, Present: []string{"other"},
	}); err != nil || n != 1 {
		t.Fatalf("DisableMissingNativeBindings = %d, %v", n, err)
	}
	if got, err := r.GetBinding(ctx, nativeBinding.ID); err != nil || got.Enabled {
		t.Fatalf("disabled native binding = %+v, %v", got, err)
	}
	if n, err := r.DisableMissingNativeBindings(ctx, db.DisableMissingNativeBindingsParams{
		EngineID: engine.ID, Present: []string{"native_lake"},
	}); err != nil || n != 0 {
		t.Errorf("DisableMissingNativeBindings(present) = %d, %v", n, err)
	}

	blockers, err := r.CountEngineBlockers(ctx, engine.ID)
	if err != nil || blockers.ManagedBindings != 1 || blockers.RunningExecutions != 0 {
		t.Fatalf("CountEngineBlockers = %+v, %v", blockers, err)
	}
	engineImpact, err := r.CountEngineImpact(ctx, engine.ID)
	if err != nil || engineImpact.ManagedBindings != 1 || engineImpact.NativeCatalogs != 1 {
		t.Fatalf("CountEngineImpact = %+v, %v", engineImpact, err)
	}
	catalogImpact, err := r.CountCatalogImpact(ctx, catalog.ID)
	if err != nil || catalogImpact.Bindings != 1 {
		t.Fatalf("CountCatalogImpact = %+v, %v", catalogImpact, err)
	}
	connectionImpact, err := r.CountConnectionImpact(ctx, connID)
	if err != nil || connectionImpact.Catalogs != 2 {
		t.Fatalf("CountConnectionImpact = %+v, %v", connectionImpact, err)
	}
	if namespaces, err := r.CountNamespacesByCatalog(ctx, catalog.ID); err != nil || namespaces != 0 {
		t.Errorf("CountNamespacesByCatalog = %d, %v", namespaces, err)
	}

	// ── grants and requests ────────────────────────────────────────────────
	grant, err := r.UpsertWorkspaceCatalogGrant(ctx, db.UpsertWorkspaceCatalogGrantParams{
		WorkspaceID: ws.ID, CatalogID: catalog.ID, GrantedBy: ownerID,
	})
	if err != nil || grant.WorkspaceID != ws.ID {
		t.Fatalf("UpsertWorkspaceCatalogGrant = %+v, %v", grant, err)
	}
	wsGrants, err := r.ListWorkspaceCatalogGrants(ctx, ws.ID)
	if err != nil || len(wsGrants) != 1 || wsGrants[0].CatalogName != trinoNext {
		t.Fatalf("ListWorkspaceCatalogGrants = %+v, %v", wsGrants, err)
	}
	catGrants, err := r.ListCatalogGrants(ctx, catalog.ID)
	if err != nil || len(catGrants) != 1 || catGrants[0].WorkspaceName != "Topology" {
		t.Fatalf("ListCatalogGrants = %+v, %v", catGrants, err)
	}
	if rows, err := r.ListGrantedCatalogsOfWorkspace(ctx, ws.ID); err != nil || len(rows) != 1 || rows[0].ConnectionName != renamed {
		t.Fatalf("ListGrantedCatalogsOfWorkspace = %+v, %v", rows, err)
	}
	if rows, err := r.ListGrantableWorkspaces(ctx); err != nil || len(rows) != 1 || rows[0].Name != "Topology" {
		t.Fatalf("ListGrantableWorkspaces = %+v, %v", rows, err)
	}
	if n, err := r.DeleteWorkspaceCatalogGrant(ctx, db.DeleteWorkspaceCatalogGrantParams{WorkspaceID: ws.ID, CatalogID: catalog.ID}); err != nil || n != 1 {
		t.Fatalf("DeleteWorkspaceCatalogGrant = %d, %v", n, err)
	}
	if n, err := r.DeleteWorkspaceCatalogGrant(ctx, db.DeleteWorkspaceCatalogGrantParams{WorkspaceID: ws.ID, CatalogID: catalog.ID}); err != nil || n != 0 {
		t.Errorf("second DeleteWorkspaceCatalogGrant = %d, %v", n, err)
	}

	req, err := r.CreateCatalogGrantRequest(ctx, db.CreateCatalogGrantRequestParams{
		WorkspaceID: ws.ID, CatalogID: catalog.ID, InitiatedBySide: "workspace", RequestedBy: ownerID,
	})
	if err != nil || req.InitiatedBySide != "workspace" {
		t.Fatalf("CreateCatalogGrantRequest = %+v, %v", req, err)
	}
	// An initiation from the other side flips the pending request.
	flipped, err := r.CreateCatalogGrantRequest(ctx, db.CreateCatalogGrantRequestParams{
		WorkspaceID: ws.ID, CatalogID: catalog.ID, InitiatedBySide: "catalog", RequestedBy: ownerID,
	})
	if err != nil || flipped.InitiatedBySide != "catalog" {
		t.Fatalf("flipped request = %+v, %v", flipped, err)
	}
	if got, err := r.GetCatalogGrantRequest(ctx, db.GetCatalogGrantRequestParams{WorkspaceID: ws.ID, CatalogID: catalog.ID}); err != nil || got.InitiatedBySide != "catalog" {
		t.Errorf("GetCatalogGrantRequest = %+v, %v", got, err)
	}
	if rows, err := r.ListCatalogGrantRequestsForWorkspace(ctx, ws.ID); err != nil || len(rows) != 1 || rows[0].CatalogName != next {
		t.Errorf("ListCatalogGrantRequestsForWorkspace = %+v, %v", rows, err)
	}
	if rows, err := r.ListCatalogGrantRequestsForCatalog(ctx, catalog.ID); err != nil || len(rows) != 1 || rows[0].WorkspaceName != "Topology" {
		t.Errorf("ListCatalogGrantRequestsForCatalog = %+v, %v", rows, err)
	}
	if n, err := r.DeleteCatalogGrantRequest(ctx, db.DeleteCatalogGrantRequestParams{WorkspaceID: ws.ID, CatalogID: catalog.ID}); err != nil || n != 1 {
		t.Fatalf("DeleteCatalogGrantRequest = %d, %v", n, err)
	}

	// ── deletes ────────────────────────────────────────────────────────────
	if err := r.DeleteBinding(ctx, binding.ID); err != nil {
		t.Fatalf("DeleteBinding: %v", err)
	}
	if _, err := r.GetBinding(ctx, binding.ID); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("deleted binding = %v, want pgx.ErrNoRows", err)
	}
	if err := r.DeleteCatalog(ctx, catalog.ID); err != nil {
		t.Fatalf("DeleteCatalog: %v", err)
	}
	if _, err := r.GetCatalogByID(ctx, catalog.ID); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("deleted catalog = %v, want pgx.ErrNoRows", err)
	}
	if err := r.DeleteNativeCatalogsOfEngine(ctx, engine.ID); err != nil {
		t.Fatalf("DeleteNativeCatalogsOfEngine: %v", err)
	}
	if _, err := r.GetCatalogByID(ctx, native.ID); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("native catalog after delete = %v, want pgx.ErrNoRows", err)
	}
	if err := gdb.Model(&model.TrinoCluster{}).Where("engine_id = ?", UUIDString(engine.ID)).Update("engine_id", nil).Error; err != nil {
		t.Fatalf("unlink cluster: %v", err)
	}
	if err := r.DeleteEngine(ctx, engine.ID); err != nil {
		t.Fatalf("DeleteEngine: %v", err)
	}
	if _, err := r.GetEngineByID(ctx, engine.ID); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("deleted engine = %v, want pgx.ErrNoRows", err)
	}
	if err := r.DeleteCatalogConnection(ctx, connID); err != nil {
		t.Fatalf("DeleteCatalogConnection: %v", err)
	}
	if _, err := r.GetCatalogConnectionByID(ctx, connID); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("deleted connection = %v, want pgx.ErrNoRows", err)
	}
}

// TestCatalogConnectionUniqueName checks the store surfaces a duplicate name as
// the same PostgreSQL unique violation the service maps to ErrNameTaken.
func TestCatalogConnectionUniqueName(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	sqlDB := stdlib.OpenDBFromPool(pool)
	gdb, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{
		Logger:         gormlogger.Discard,
		TranslateError: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	ctx := context.Background()
	r := New(gdb)
	owner := createAPIKeyUser(t, gdb, "topology-dup@example.test")
	ownerID := PGUUID(string(owner.ID))
	create := func(name string) error {
		_, err := r.CreateCatalogConnection(ctx, db.CreateCatalogConnectionParams{
			ID:   pgtype.UUID{Bytes: uuid.New(), Valid: true},
			Name: name, Type: "polaris", Url: "http://polaris.test", CreatedBy: ownerID,
		})
		return err
	}
	if err := create("dup"); err != nil {
		t.Fatalf("first create: %v", err)
	}
	err = create("dup")
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		t.Fatalf("duplicate create = %v, want a 23505 unique violation", err)
	}
}
