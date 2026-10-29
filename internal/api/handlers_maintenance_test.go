package api

import (
	"context"
	"reflect"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/audit"
	"github.com/galileostudio/baleia/internal/config"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/queryengine"
	"github.com/galileostudio/baleia/internal/queryengine/fake"
	"github.com/galileostudio/baleia/internal/testdb"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// maintEngine is a fake engine that supports Iceberg maintenance.
type maintEngine struct{ *fake.Engine }

func (e maintEngine) Capabilities() queryengine.Capabilities {
	c := e.Engine.Capabilities()
	c.IcebergMaintenance = true
	return c
}

type maintEngines struct{}

func (maintEngines) Get(context.Context, pgtype.UUID) (queryengine.Engine, error) {
	return maintEngine{fake.New(fake.Options{})}, nil
}

func (maintEngines) DefaultTrino(context.Context) (queryengine.Engine, error) {
	return maintEngine{fake.New(fake.Options{})}, nil
}

// auditLog keeps the events handlers record.
type auditLog struct {
	mu     sync.Mutex
	events []audit.Event
}

func (a *auditLog) Record(_ context.Context, e audit.Event) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.events = append(a.events, e)
	return nil
}

func (a *auditLog) actions() []audit.Action {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]audit.Action, 0, len(a.events))
	for _, e := range a.events {
		out = append(out, e.Action)
	}
	return out
}

type maintenanceFixture struct {
	pool      *pgxpool.Pool
	h         *API
	audit     *auditLog
	catalogID pgtype.UUID
	bindingID pgtype.UUID
	admin     context.Context
	stranger  context.Context
}

// newMaintenanceFixture is one Iceberg (Polaris) catalog with a binding of
// the given mode on a maintenance-capable engine, and a 1h retention floor.
func newMaintenanceFixture(t *testing.T, bindingMode string) *maintenanceFixture {
	t.Helper()
	pool := testdb.New(t)
	ctx := context.Background()
	f := &maintenanceFixture{pool: pool, audit: &auditLog{}}
	user := func(email, role string) string {
		var id pgtype.UUID
		if err := pool.QueryRow(ctx, `INSERT INTO users (name, email, role, password_hash) VALUES ($1, $1, $2, 'x') RETURNING id`,
			email, role).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return idString(id)
	}
	adminID := user("admin@maint.test", "admin")
	f.admin = ctxAs(adminID, "admin")
	f.stranger = ctxAs(user("stranger@maint.test", "member"), "member")
	var conn, engConn, engine pgtype.UUID
	for _, c := range []struct {
		name string
		dst  *pgtype.UUID
	}{{"maint-source", &conn}, {"maint-engine", &engConn}} {
		if err := pool.QueryRow(ctx, `INSERT INTO catalog_connections (name, type, url, created_by) VALUES ($1, 'polaris', 'http://polaris.test', $2) RETURNING id`,
			c.name, adminID).Scan(c.dst); err != nil {
			t.Fatal(err)
		}
	}
	if err := pool.QueryRow(ctx, `INSERT INTO catalogs (connection_id, name, trino_catalog_name, cluster_id, warehouse, connector)
		VALUES ($1, 'lake', 'lake', (SELECT id FROM trino_clusters LIMIT 1), 'wh', 'iceberg') RETURNING id`, conn).Scan(&f.catalogID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO engines (name, kind, class, connection_id, identity_mode, created_by)
		VALUES ('maint_engine', 'trino', 'federated', $1, 'connection', $2) RETURNING id`, engConn, adminID).Scan(&engine); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO catalog_bindings (catalog_id, engine_id, engine_catalog_name, mode, enabled, lifecycle_state)
		VALUES ($1, $2, 'lake', $3, true, 'active') RETURNING id`, f.catalogID, engine, bindingMode).Scan(&f.bindingID); err != nil {
		t.Fatal(err)
	}
	// The engine's cluster has loaded the catalog with the projected 1h
	// floor; an adopted binding's registry row is irrelevant.
	if _, err := pool.Exec(ctx, `UPDATE trino_clusters SET engine_id = $1 WHERE id = (SELECT cluster_id FROM catalogs WHERE id = $2)`, engine, f.catalogID); err != nil {
		t.Fatal(err)
	}
	f.setApplied(t, `{"iceberg.expire-snapshots.min-retention": "1h", "iceberg.remove-orphan-files.min-retention": "1h"}`, "trino")
	f.h = testAPI(handlerFields{
		queries:  db.New(pool),
		pool:     pool,
		tx:       poolTx{pool: pool},
		engines:  maintEngines{},
		audit:    f.audit,
		settings: SettingsFromConfig(&config.Config{IcebergMaintenanceMinRetention: time.Hour}),
	})
	return f
}

// setApplied writes what the engine's cluster holds for the catalog.
func (f *maintenanceFixture) setApplied(t *testing.T, props, updatedBy string) {
	t.Helper()
	if _, err := f.pool.Exec(context.Background(), `INSERT INTO trino_catalog_registry (cluster_id, catalog_name, connector_name, properties, updated_by)
		VALUES ((SELECT cluster_id FROM catalogs WHERE id = $1), 'lake', 'iceberg', $2::jsonb, $3)
		ON CONFLICT (cluster_id, catalog_name) DO UPDATE SET properties = EXCLUDED.properties, updated_by = EXCLUDED.updated_by`,
		f.catalogID, props, updatedBy); err != nil {
		t.Fatal(err)
	}
}

func (f *maintenanceFixture) catalog() openapi_types.UUID {
	return openapi_types.UUID(f.catalogID.Bytes)
}

func ptrInt64(v int64) *int64 { return &v }

// expiryParams expires snapshots past 100, keeps 10, with the retention given.
func expiryParams(retention int64) MaintenancePolicy {
	off := MaintenanceSettings{Enabled: false, OnConflict: "retry_with_backoff"}
	return MaintenancePolicy{
		Mode:                        "managed",
		WriterCommitIntervalSeconds: 60,
		Optimize:                    off,
		OptimizeManifests:           off,
		RemoveOrphanFiles:           off,
		ExpireSnapshots: MaintenanceSettings{
			Enabled: true, MinIntervalSeconds: 3600, TimeoutSeconds: 600, OnConflict: "retry_with_backoff",
			Trigger: MaintenanceTrigger{Snapshots: ptrInt64(100)},
			Params:  MaintenanceParams{RetentionSeconds: ptrInt64(retention), RetainLast: ptrInt64(10)},
		},
	}
}

func (f *maintenanceFixture) put(ctx context.Context, t *testing.T, body MaintenancePolicy, ifMatch *int64) PutTableMaintenanceResponseObject {
	t.Helper()
	req := PutTableMaintenanceRequestObject{CatalogId: f.catalog(), Namespace: "cdc", Table: "orders", Body: &body}
	if ifMatch != nil {
		s := strconv.FormatInt(*ifMatch, 10)
		req.Params.IfMatch = &s
	}
	resp, err := f.h.PutTableMaintenance(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// TestTableMaintenance_Parameters: create, refuse a second create, refuse a
// stale revision, replace, read, remove; every change audited (#500).
func TestTableMaintenance_Parameters(t *testing.T) {
	t.Parallel()
	f := newMaintenanceFixture(t, "managed")

	created, ok := f.put(f.admin, t, expiryParams(3600), nil).(PutTableMaintenance200JSONResponse)
	if !ok || created.Revision != 1 {
		t.Fatalf("create = %+v", created)
	}
	if resp, ok := f.put(f.admin, t, expiryParams(3600), nil).(PutTableMaintenance409JSONResponse); !ok || resp.Code != ErrorCodeConflict {
		t.Fatalf("second create = %+v", resp)
	}
	stale := int64(9)
	if resp, ok := f.put(f.admin, t, expiryParams(3600), &stale).(PutTableMaintenance409JSONResponse); !ok || resp.Code != ErrorCodeVersionConflict {
		t.Fatalf("stale revision = %+v", resp)
	}
	one := int64(1)
	body := expiryParams(7200)
	body.Mode = "off"
	replaced, ok := f.put(f.admin, t, body, &one).(PutTableMaintenance200JSONResponse)
	if !ok || replaced.Revision != 2 || replaced.Mode != "off" || *replaced.ExpireSnapshots.Params.RetentionSeconds != 7200 {
		t.Fatalf("replace = %+v", replaced)
	}

	got, err := f.h.GetTableMaintenance(f.admin, GetTableMaintenanceRequestObject{CatalogId: f.catalog(), Namespace: "cdc", Table: "orders"})
	if err != nil {
		t.Fatal(err)
	}
	view, ok := got.(GetTableMaintenance200JSONResponse)
	if !ok || view.Policy == nil || view.Policy.Revision != 2 || view.RetentionFloorSeconds != 3600 || view.Plan == nil || len(view.Plan.Plan) != 4 {
		t.Fatalf("get = %+v", got)
	}
	if view.AllowedActions == nil || !slices.Contains(*view.AllowedActions, TableMaintenanceAllowedActionUpdate) {
		t.Fatalf("admin allowed actions = %v", view.AllowedActions)
	}

	if resp, err := f.h.DeleteTableMaintenance(f.admin, DeleteTableMaintenanceRequestObject{CatalogId: f.catalog(), Namespace: "cdc", Table: "orders"}); err != nil || !isType[DeleteTableMaintenance204Response](resp) {
		t.Fatalf("delete = %T, %v", resp, err)
	}
	if resp, _ := f.h.DeleteTableMaintenance(f.admin, DeleteTableMaintenanceRequestObject{CatalogId: f.catalog(), Namespace: "cdc", Table: "orders"}); !isType[DeleteTableMaintenance404JSONResponse](resp) {
		t.Fatalf("second delete = %T", resp)
	}
	want := []audit.Action{audit.MaintenanceParametersSet, audit.MaintenanceParametersSet, audit.MaintenanceParametersRemoved}
	if got := f.audit.actions(); len(got) != len(want) || got[0] != want[0] || got[2] != want[2] {
		t.Fatalf("audit = %v, want %v", got, want)
	}
}

// TestTableMaintenance_Validation: field errors come back with their path,
// a managed binding accepts the projected floor, an adopted one refuses
// anything under Trino's 7 days.
func TestTableMaintenance_Validation(t *testing.T) {
	t.Parallel()
	f := newMaintenanceFixture(t, "managed")
	bad := expiryParams(3600)
	bad.ExpireSnapshots.MinIntervalSeconds = 10
	resp, ok := f.put(f.admin, t, bad, nil).(PutTableMaintenance422JSONResponse)
	if !ok || resp.Fields == nil || len(*resp.Fields) != 1 || (*resp.Fields)[0].Field != "expire_snapshots.min_interval_seconds" {
		t.Fatalf("invalid = %+v", resp)
	}
	if resp, ok := f.put(f.admin, t, expiryParams(600), nil).(PutTableMaintenance422JSONResponse); !ok {
		t.Fatalf("below the 1h floor = %T", resp)
	}

	// The engine still holds Trino's 7 days (before reconcile): 1h is
	// refused as out of range, with the floor it has now.
	stale := newMaintenanceFixture(t, "managed")
	stale.setApplied(t, `{}`, "trino")
	resp, ok = stale.put(stale.admin, t, expiryParams(3600), nil).(PutTableMaintenance422JSONResponse)
	if !ok || resp.Fields == nil || (*resp.Fields)[0].Field != "expire_snapshots.params.retention_seconds" || (*(*resp.Fields)[0].Params)["min"] != "604800" {
		t.Fatalf("not yet reconciled, 1h = %+v", resp)
	}

	adopted := newMaintenanceFixture(t, "adopted")
	if resp, ok := adopted.put(adopted.admin, t, expiryParams(3600), nil).(PutTableMaintenance409JSONResponse); !ok || resp.Code != ErrorCodeCatalogNotManaged {
		t.Fatalf("adopted binding, 1h = %+v", resp)
	}
	if _, ok := adopted.put(adopted.admin, t, expiryParams(7*86400), nil).(PutTableMaintenance200JSONResponse); !ok {
		t.Fatal("adopted binding, 7 days must be accepted")
	}
}

// TestTableMaintenance_Access: a member without access to the catalog sees
// neither the table's maintenance nor can change it.
func TestTableMaintenance_Access(t *testing.T) {
	t.Parallel()
	f := newMaintenanceFixture(t, "managed")
	if resp := f.put(f.stranger, t, expiryParams(3600), nil); !isType[PutTableMaintenance404JSONResponse](resp) {
		t.Fatalf("stranger put = %T", resp)
	}
	resp, err := f.h.GetTableMaintenance(f.stranger, GetTableMaintenanceRequestObject{CatalogId: f.catalog(), Namespace: "cdc", Table: "orders"})
	if err != nil || !isType[GetTableMaintenance404JSONResponse](resp) {
		t.Fatalf("stranger get = %T, %v", resp, err)
	}
}

// TestTableMaintenance_RunsAndPlans: dry run, manual runs (one at a time),
// a table maintained elsewhere, the catalog plan, health's mode, and the
// legacy route's conflict.
func TestTableMaintenance_RunsAndPlans(t *testing.T) {
	t.Parallel()
	f := newMaintenanceFixture(t, "managed")
	ctx := context.Background()
	f.put(f.admin, t, expiryParams(3600), nil)
	if _, err := f.pool.Exec(ctx, `INSERT INTO table_health_snapshots (catalog_id, catalog_connection_id, catalog, namespace, table_name, snapshot_count, health_score)
		SELECT id, connection_id, 'lake', 'cdc', 'orders', 140, 70 FROM catalogs WHERE id = $1`, f.catalogID); err != nil {
		t.Fatal(err)
	}
	table := func() (openapi_types.UUID, string, string) { return f.catalog(), "cdc", "orders" }

	c, ns, tb := table()
	dry, err := f.h.DryRunTableMaintenance(f.admin, DryRunTableMaintenanceRequestObject{CatalogId: c, Namespace: ns, Table: tb})
	if err != nil {
		t.Fatal(err)
	}
	plan, ok := dry.(DryRunTableMaintenance200JSONResponse)
	if !ok {
		t.Fatalf("dry run = %T", dry)
	}
	var wouldRun string
	for _, s := range plan.Plan {
		if s.WouldRun {
			wouldRun = string(s.Operation)
		}
	}
	if wouldRun != "expire_snapshots" {
		t.Fatalf("dry run plan = %+v", plan.Plan)
	}

	run := func() RunTableMaintenanceResponseObject {
		resp, err := f.h.RunTableMaintenance(f.admin, RunTableMaintenanceRequestObject{CatalogId: c, Namespace: ns, Table: tb,
			Body: &ManualMaintenanceRequest{Operation: "next_due"}})
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	started, ok := run().(RunTableMaintenance202JSONResponse)
	if !ok || started.Trigger != "manual" || started.Operations[0] != "expire_snapshots" || started.Reason.Fired == nil || *started.Reason.Fired != "snapshots" {
		t.Fatalf("manual run = %+v", started)
	}
	if resp, ok := run().(RunTableMaintenance409JSONResponse); !ok || resp.Code != ErrorCodeConflict {
		t.Fatalf("second run while one runs = %T", resp)
	}
	legacy, err := f.h.CreateMaintenanceRun(f.admin, CreateMaintenanceRunRequestObject{CatalogId: c,
		Body: &CreateMaintenanceRequest{Namespace: ns, Table: tb, Operations: []string{"optimize"}}})
	if err != nil || !isType[CreateMaintenanceRun409JSONResponse](legacy) {
		t.Fatalf("legacy run while one runs = %T, %v", legacy, err)
	}

	runs, err := f.h.ListMaintenanceRuns(f.admin, ListMaintenanceRunsRequestObject{CatalogId: c,
		Params: ListMaintenanceRunsParams{Trigger: ptrTo(ListMaintenanceRunsParamsTrigger("dry_run"))}})
	if err != nil {
		t.Fatal(err)
	}
	if list := runs.(ListMaintenanceRuns200JSONResponse); len(list) != 1 || list[0].Status != "dry_run" {
		t.Fatalf("dry runs = %+v", list)
	}

	catalogPlan, err := f.h.GetCatalogMaintenancePlan(f.admin, GetCatalogMaintenancePlanRequestObject{CatalogId: c})
	if err != nil {
		t.Fatal(err)
	}
	if items := catalogPlan.(GetCatalogMaintenancePlan200JSONResponse); len(items) != 1 || items[0].LastRun == nil || items[0].Mode != "managed" {
		t.Fatalf("catalog plan = %+v", items)
	}
	// The catalog plan reads catalog-wide; it must say what the table's own
	// plan says (a run is in progress here, so every step is blocked).
	view, err := f.h.GetTableMaintenance(f.admin, GetTableMaintenanceRequestObject{CatalogId: c, Namespace: ns, Table: tb})
	if err != nil {
		t.Fatal(err)
	}
	own := view.(GetTableMaintenance200JSONResponse).Plan
	batched := catalogPlan.(GetCatalogMaintenancePlan200JSONResponse)[0].Plan.Plan
	if own == nil || len(own.Plan) != len(batched) {
		t.Fatalf("own plan = %+v, catalog plan = %+v", own, batched)
	}
	for i := range batched {
		a, b := own.Plan[i], batched[i]
		if a.Operation != b.Operation || a.Due != b.Due || a.WouldRun != b.WouldRun || !reflect.DeepEqual(a.Reason, b.Reason) {
			t.Fatalf("step %d: own %+v, catalog %+v", i, a, b)
		}
	}
	health, err := f.h.ListTableHealth(f.admin, ListTableHealthRequestObject{CatalogId: c})
	if err != nil {
		t.Fatal(err)
	}
	if rows := health.(ListTableHealth200JSONResponse); len(rows) != 1 || rows[0].MaintenanceMode == nil || *rows[0].MaintenanceMode != "managed" {
		t.Fatalf("health = %+v", rows)
	}

	two := int64(1)
	elsewhere := expiryParams(3600)
	elsewhere.Mode = "maintained_elsewhere"
	f.put(f.admin, t, elsewhere, &two)
	dry, _ = f.h.DryRunTableMaintenance(f.admin, DryRunTableMaintenanceRequestObject{CatalogId: c, Namespace: ns, Table: tb})
	if resp, ok := dry.(DryRunTableMaintenance409JSONResponse); !ok || resp.Code != ErrorCodeMaintenanceExternal {
		t.Fatalf("dry run elsewhere = %T", dry)
	}
	if resp, ok := run().(RunTableMaintenance409JSONResponse); !ok || resp.Code != ErrorCodeMaintenanceExternal {
		t.Fatalf("run elsewhere = %T", resp)
	}
}

// TestTableMaintenance_Analyze: analyze now accepts an enabled Iceberg
// catalog and refuses an unknown one.
func TestTableMaintenance_Analyze(t *testing.T) {
	t.Parallel()
	f := newMaintenanceFixture(t, "managed")
	resp, err := f.h.AnalyzeTableHealth(f.admin, AnalyzeTableHealthRequestObject{CatalogId: f.catalog(), Namespace: "cdc", Table: "orders"})
	if err != nil || !isType[AnalyzeTableHealth202Response](resp) {
		t.Fatalf("analyze table = %T, %v", resp, err)
	}
	if _, err := f.pool.Exec(context.Background(), `UPDATE catalogs SET enabled = false WHERE id = $1`, f.catalogID); err != nil {
		t.Fatal(err)
	}
	catalog, err := f.h.AnalyzeCatalogTableHealth(f.admin, AnalyzeCatalogTableHealthRequestObject{CatalogId: f.catalog()})
	if err != nil || !isType[AnalyzeCatalogTableHealth422JSONResponse](catalog) {
		t.Fatalf("analyze a disabled catalog = %T, %v", catalog, err)
	}
}

func ptrTo[T any](v T) *T { return &v }
