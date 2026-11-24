package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/catalogs"
	"github.com/galileostudio/baleia/internal/config"
	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/crypto"
	"github.com/galileostudio/baleia/internal/datapolicy"
	"github.com/galileostudio/baleia/internal/datapolicy/viewsql"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/queryengine"
	"github.com/galileostudio/baleia/internal/queryengine/fake"
	"github.com/galileostudio/baleia/internal/store/model"
	"github.com/galileostudio/baleia/internal/store/repo"
	"github.com/galileostudio/baleia/internal/testdb"
	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"gorm.io/gorm"
)

// typedEngine is a fake engine that types every expression it is asked
// about: a filter is boolean, a mask keeps its column's varchar.
type typedEngine struct{ *fake.Engine }

func (typedEngine) ExpressionTypes(_ context.Context, _ []string, exprs []string) ([]string, error) {
	if len(exprs) == 1 {
		return []string{"boolean"}, nil
	}
	return []string{"varchar", "varchar"}, nil
}

// typedEngines serves a typedEngine for every engine but the down ones.
type typedEngines struct{ down map[db.UUID]bool }

func (e typedEngines) Get(_ context.Context, id db.UUID) (queryengine.Engine, error) {
	if e.down[id] {
		return nil, errors.New("engine unavailable")
	}
	return typedEngine{fake.New(fake.Options{})}, nil
}

func (typedEngines) DefaultTrino(context.Context) (queryengine.Engine, error) {
	return typedEngine{fake.New(fake.Options{})}, nil
}

// bundleObjects keeps bundles in memory.
type bundleObjects struct {
	mu      sync.Mutex
	objects map[string][]byte
}

func (o *bundleObjects) Put(_ context.Context, key string, data []byte, _ string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.objects == nil {
		o.objects = map[string][]byte{}
	}
	o.objects[key] = data
	return nil
}

func (o *bundleObjects) GetReader(_ context.Context, key string) (io.ReadCloser, string, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	data, ok := o.objects[key]
	if !ok {
		return nil, "", errors.New("no such object")
	}
	return io.NopCloser(bytes.NewReader(data)), "application/gzip", nil
}

type dataPolicyFixture struct {
	gdb      *gorm.DB
	h        *API
	audit    *auditLog
	catalog  openapi_types.UUID
	engineID db.UUID
	admin    context.Context
	member   context.Context
	objects  *bundleObjects
	views    datapolicy.ViewInspection
}

// newDataPolicyFixture is one Polaris catalog bound as "lake" on a Trino
// engine that enforces data policies, owned by an admin, with data policies
// on for the deployment.
func newDataPolicyFixture(t *testing.T) *dataPolicyFixture {
	t.Helper()
	gdb := testdb.New(t)
	ctx := context.Background()
	q := repo.New(gdb)
	f := &dataPolicyFixture{gdb: gdb, audit: &auditLog{}, objects: &bundleObjects{}}
	user := func(email, role string) string {
		password := "x"
		row := model.User{ID: model.UUID(repo.NewUUID()), Name: email, Email: model.Text(email), Role: model.Text(role), PasswordHash: &password}
		if err := gdb.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
		return string(row.ID)
	}
	adminID := user("admin@policies.test", "admin")
	f.admin = ctxAs(adminID, "admin")
	f.member = ctxAs(user("member@policies.test", "member"), "member")
	adminUUID := repo.PGUUID(adminID)
	conn, err := q.CreateCatalogConnection(ctx, db.CreateCatalogConnectionParams{
		ID: repo.PGUUID(repo.NewUUID()), Name: "policies-source", Type: "polaris",
		Url: "http://polaris.test", Config: []byte(`{"url":"http://polaris.test"}`), CreatedBy: adminUUID,
	})
	if err != nil {
		t.Fatal(err)
	}
	engConn, err := q.CreateCatalogConnection(ctx, db.CreateCatalogConnectionParams{
		ID: repo.PGUUID(repo.NewUUID()), Name: "policies-engine", Type: "trino",
		Url: "http://trino.test", CreatedBy: adminUUID,
	})
	if err != nil {
		t.Fatal(err)
	}
	var cluster model.TrinoCluster
	if err := gdb.Order("name").First(&cluster).Error; err != nil {
		t.Fatal(err)
	}
	trinoCatalog, warehouse, connector, clusterID := model.Text("lake"), "wh", model.Text("iceberg"), cluster.ID
	catalog := model.Catalog{
		ID: model.UUID(repo.NewUUID()), ConnectionID: model.UUID(repo.UUIDString(conn.ID)), Name: "lake",
		TrinoCatalogName: &trinoCatalog, ClusterID: &clusterID, Warehouse: &warehouse, Connector: &connector, Origin: "source",
	}
	if err := gdb.Create(&catalog).Error; err != nil {
		t.Fatal(err)
	}
	f.catalog = openapi_types.UUID(uuid.MustParse(string(catalog.ID)))
	engine, err := q.CreateEngine(ctx, db.CreateEngineParams{
		Name: "policies_engine", Kind: "trino", Class: "federated", ConnectionID: engConn.ID,
		IdentityMode: "connection", CreatedBy: adminUUID,
	})
	if err != nil {
		t.Fatal(err)
	}
	f.engineID = engine.ID
	on := true
	if _, err := q.UpdateEngine(ctx, db.UpdateEngineParams{ID: engine.ID, DataPolicies: &on}); err != nil {
		t.Fatal(err)
	}
	isoConnector := "iceberg"
	binding := model.CatalogBinding{
		ID: model.UUID(repo.NewUUID()), CatalogID: catalog.ID, EngineID: model.UUID(engine.ID.String()),
		EngineCatalogName: "lake", Connector: &isoConnector, Mode: "managed", Enabled: true, LifecycleState: "active",
	}
	if err := gdb.Create(&binding).Error; err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		DataPolicies: config.DataPoliciesOPA, DataPolicyLease: time.Hour,
		TrinoAdminUser: "baleia_admin", TrinoServiceUser: "baleia", TrinoUserIdentity: "uuid",
	}
	f.h = testAPI(handlerFields{
		store: gdb, connections: testRegistry(t), engines: typedEngines{}, audit: f.audit,
		settings: SettingsFromConfig(cfg), tx: storeTx{db: gdb},
	})
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f.h.publisher = &datapolicy.Publisher{
		Store: f.h.policies, Objects: f.objects, Key: key, Lease: time.Hour, IdentityMode: "uuid",
		Trusted: map[string]bool{"baleia_admin": true, "baleia": true},
	}
	f.h.views = func(context.Context) (datapolicy.ViewInspection, error) { return f.views, nil }
	return f
}

func rowFilterBody(name, expr string) CreateDataPolicyRequest {
	mode := DataPolicyModePermissive
	return CreateDataPolicyRequest{
		Name: name, Kind: DataPolicyKindRowFilter, Schema: "Sales", Table: "Orders", Mode: &mode,
		Subjects: []DataPolicySubject{{Team: ptr(openapi_types.UUID(uuid.New()))}}, Expression: expr,
	}
}

func (f *dataPolicyFixture) create(t *testing.T, ctx context.Context, body CreateDataPolicyRequest) (int, string, DataPolicy) { //nolint:revive // thelper wants t first
	t.Helper()
	resp, err := f.h.CreateDataPolicy(ctx, CreateDataPolicyRequestObject{Id: f.catalog, Body: &body})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	if err := resp.VisitCreateDataPolicyResponse(rec); err != nil {
		t.Fatal(err)
	}
	created, _ := resp.(CreateDataPolicy201JSONResponse)
	return rec.Code, rec.Body.String(), DataPolicy(created)
}

func TestDataPolicies_Authoring(t *testing.T) {
	t.Parallel()
	f := newDataPolicyFixture(t)
	ctx := f.admin

	code, body, row := f.create(t, ctx, rowFilterBody("eu", "region = 'EU'"))
	if code != 201 || row.Validation != DataPolicyValidationValid || row.Compiled == nil || *row.Compiled != "region = 'EU'" {
		t.Fatalf("create = %d %s", code, body)
	}
	if row.Schema != "sales" || row.Table != "orders" || row.Version != 1 {
		t.Fatalf("created = %+v, want a lowercase target at version 1", row)
	}
	maskTemplate := DataPolicyTemplateNull
	mask := CreateDataPolicyRequest{
		Name: "hide-email", Kind: DataPolicyKindColumnMask, Schema: "sales", Table: "orders", Column: ptr("email"),
		Priority: ptr(int32(1)), Template: &maskTemplate,
		Subjects:   []DataPolicySubject{{Everyone: ptr(true), Except: &[]DataPolicySubject{{Group: ptr("auditors")}}}},
		Expression: "CAST(NULL AS varchar)",
	}
	if code, body, created := f.create(t, ctx, mask); code != 201 || created.Validation != DataPolicyValidationValid ||
		created.Template == nil || *created.Template != DataPolicyTemplateNull {
		t.Fatalf("create mask = %d %s", code, body)
	}

	// An expression outside the language is kept as an invalid draft.
	code, body, bad := f.create(t, ctx, rowFilterBody("mine", "owner = current_user"))
	if code != 201 || bad.Validation != DataPolicyValidationInvalid || bad.InvalidReason == nil || *bad.InvalidReason != "function_not_allowed" {
		t.Fatalf("invalid draft = %d %s", code, body)
	}

	if code, body, _ := f.create(t, ctx, rowFilterBody("eu", "region = 'UK'")); code != 409 || !strings.Contains(body, "name_in_use") {
		t.Fatalf("duplicate name = %d %s", code, body)
	}
	invalid := rowFilterBody("Bad Name", "region = 'EU'")
	invalid.Mode = nil
	if code, body, _ := f.create(t, ctx, invalid); code != 422 || !strings.Contains(body, `"field":"name"`) || !strings.Contains(body, `"field":"mode"`) {
		t.Fatalf("invalid fields = %d %s", code, body)
	}

	listResp, err := f.h.ListCatalogDataPolicies(ctx, ListCatalogDataPoliciesRequestObject{Id: f.catalog})
	if err != nil {
		t.Fatal(err)
	}
	list, ok := listResp.(ListCatalogDataPolicies200JSONResponse)
	if !ok || len(list.Policies) != 3 || !list.Enabled || !list.Draft || len(list.Names) != 1 || list.Names[0] != "lake" {
		t.Fatalf("list = %+v", listResp)
	}

	update := UpdateDataPolicyRequest{
		Name: "eu", Kind: DataPolicyKindRowFilter, Schema: "sales", Table: "orders", Mode: ptr(DataPolicyModeRestrictive),
		Subjects: row.Subjects, Expression: "region IN ('EU', 'UK')", Version: 1,
	}
	updResp, err := f.h.UpdateDataPolicy(ctx, UpdateDataPolicyRequestObject{Id: row.Id, Body: &update})
	if err != nil {
		t.Fatal(err)
	}
	updated, ok := updResp.(UpdateDataPolicy200JSONResponse)
	if !ok || updated.Version != 2 || *updated.Mode != DataPolicyModeRestrictive {
		t.Fatalf("update = %+v", updResp)
	}
	if resp, _ := f.h.UpdateDataPolicy(ctx, UpdateDataPolicyRequestObject{Id: row.Id, Body: &update}); resp == nil {
		t.Fatal("stale update returned nothing")
	} else if _, conflict := resp.(UpdateDataPolicy409JSONResponse); !conflict {
		t.Fatalf("stale update = %+v, want 409 version_conflict", resp)
	}

	if resp, _ := f.h.DeleteDataPolicy(ctx, DeleteDataPolicyRequestObject{Id: bad.Id}); resp == nil {
		t.Fatal("delete returned nothing")
	} else if _, ok := resp.(DeleteDataPolicy204Response); !ok {
		t.Fatalf("delete = %+v", resp)
	}
	if resp, _ := f.h.GetDataPolicy(ctx, GetDataPolicyRequestObject{Id: bad.Id}); resp == nil {
		t.Fatal("get returned nothing")
	} else if _, ok := resp.(GetDataPolicy404JSONResponse); !ok {
		t.Fatalf("get after delete = %+v", resp)
	}
}

// TestDataPolicies_TypeCheckOnAnEngineThatAnswers: an engine that cannot be
// asked does not keep a policy pending while another engine serving the
// catalog can type-check it.
func TestDataPolicies_TypeCheckOnAnEngineThatAnswers(t *testing.T) {
	t.Parallel()
	f := newDataPolicyFixture(t)
	ctx := context.Background()
	f.h.engines = typedEngines{down: map[db.UUID]bool{f.engineID: true}}
	if code, body, row := f.create(t, f.admin, rowFilterBody("eu", "region = 'EU'")); code != 201 || row.Validation != DataPolicyValidationPending {
		t.Fatalf("with the only engine down = %d %s", code, body)
	}

	q := repo.New(f.gdb)
	first, err := q.GetEngineByID(ctx, f.engineID)
	if err != nil {
		t.Fatal(err)
	}
	engConn, err := q.CreateCatalogConnection(ctx, db.CreateCatalogConnectionParams{
		ID: repo.PGUUID(repo.NewUUID()), Name: "policies-engine-2", Type: "trino",
		Url: "http://trino-2.test", CreatedBy: first.CreatedBy,
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := q.CreateEngine(ctx, db.CreateEngineParams{
		Name: "policies_engine_2", Kind: "trino", Class: "federated", ConnectionID: engConn.ID,
		IdentityMode: "connection", CreatedBy: first.CreatedBy,
	})
	if err != nil {
		t.Fatal(err)
	}
	on := true
	if _, err := q.UpdateEngine(ctx, db.UpdateEngineParams{ID: second.ID, DataPolicies: &on}); err != nil {
		t.Fatal(err)
	}
	connector := "iceberg"
	binding := model.CatalogBinding{
		ID: model.UUID(repo.NewUUID()), CatalogID: model.UUID(uuid.UUID(f.catalog).String()), EngineID: model.UUID(second.ID.String()),
		EngineCatalogName: "lake", Connector: &connector, Mode: "managed", Enabled: true, LifecycleState: "active",
	}
	if err := f.gdb.Create(&binding).Error; err != nil {
		t.Fatal(err)
	}
	if code, body, row := f.create(t, f.admin, rowFilterBody("us", "region = 'US'")); code != 201 || row.Validation != DataPolicyValidationValid {
		t.Fatalf("with a second engine up = %d %s", code, body)
	}
}

func TestDataPolicies_AuthoringRefusals(t *testing.T) {
	t.Parallel()
	f := newDataPolicyFixture(t)

	if code, _, _ := f.create(t, f.member, rowFilterBody("eu", "region = 'EU'")); code != 404 {
		t.Fatalf("a member who does not own the catalog = %d, want 404", code)
	}

	f.h.settings.DataPolicy.Enabled = false
	if code, body, _ := f.create(t, f.admin, rowFilterBody("eu", "region = 'EU'")); code != 409 || !strings.Contains(body, "data_policies_disabled") {
		t.Fatalf("deployment off = %d %s", code, body)
	}
	f.h.settings.DataPolicy.Enabled = true

	off := false
	if err := f.gdb.Model(&model.Engine{}).Where("id = ?", f.engineID.String()).Update("data_policies", off).Error; err != nil {
		t.Fatal(err)
	}
	if code, body, _ := f.create(t, f.admin, rowFilterBody("eu", "region = 'EU'")); code != 422 || !strings.Contains(body, "data_policies_unenforced") {
		t.Fatalf("engine without data policies = %d %s", code, body)
	}
}

func TestDataPolicies_PublishAndRollback(t *testing.T) {
	t.Parallel()
	f := newDataPolicyFixture(t)
	ctx := f.admin
	if code, body, _ := f.create(t, ctx, rowFilterBody("eu", "region = 'EU'")); code != 201 {
		t.Fatalf("create = %d %s", code, body)
	}

	publish := func(ctx context.Context) PublishCatalogDataPoliciesResponseObject {
		t.Helper()
		resp, err := f.h.PublishCatalogDataPolicies(ctx, PublishCatalogDataPoliciesRequestObject{Id: f.catalog})
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	first, ok := publish(ctx).(PublishCatalogDataPolicies201JSONResponse)
	if !ok || !first.Published || first.Number != 1 || first.Reason != DataPolicyRevisionReasonPublish {
		t.Fatalf("publish = %+v", first)
	}
	if _, stored := f.objects.objects[datapolicy.BundleKey(uuid.UUID(first.Id))]; !stored {
		t.Fatal("the bundle was not stored")
	}
	listResp, _ := f.h.ListCatalogDataPolicies(ctx, ListCatalogDataPoliciesRequestObject{Id: f.catalog})
	if list := listResp.(ListCatalogDataPolicies200JSONResponse); list.Draft {
		t.Fatal("the catalog still has a draft after publishing")
	}

	// A trusted-owned DEFINER view over the policied table refuses the publish.
	f.views = datapolicy.ViewInspection{Views: []datapolicy.CatalogView{viewsql.Resolve([]string{"lake"}, connections.View{
		Namespace: connections.Namespace{"sales"}, Name: "v_all", Owner: "baleia_admin", SQL: "SELECT * FROM orders",
	})}}
	refused, ok := publish(ctx).(PublishCatalogDataPolicies422JSONResponse)
	if !ok || len(refused.Problems) != 1 || refused.Problems[0].Code != DataPolicyProblemTrustedDefinerView || refused.Code != ErrorCodeDataPolicyInvalid {
		t.Fatalf("publish with a trusted DEFINER view = %+v", refused)
	}
	viewsResp, err := f.h.ListCatalogDefinerViews(ctx, ListCatalogDefinerViewsRequestObject{Id: f.catalog})
	if err != nil {
		t.Fatal(err)
	}
	if views := viewsResp.(ListCatalogDefinerViews200JSONResponse); len(views) != 1 || !views[0].Trusted || views[0].Owner != "baleia_admin" {
		t.Fatalf("definer views = %+v", views)
	}
	f.views = datapolicy.ViewInspection{}

	// Only a global admin lists revisions and rolls back.
	if resp, _ := f.h.ListDataPolicyRevisions(f.member, ListDataPolicyRevisionsRequestObject{}); resp == nil {
		t.Fatal("no response")
	} else if denied, ok := resp.(ListDataPolicyRevisions403JSONResponse); !ok || !strings.Contains(denied.Message, "only an admin") {
		t.Fatalf("member revisions = %+v", resp)
	}
	second, ok := publish(ctx).(PublishCatalogDataPolicies201JSONResponse)
	if !ok {
		t.Fatal("second publish failed")
	}
	rollResp, err := f.h.RollbackDataPolicyRevision(ctx, RollbackDataPolicyRevisionRequestObject{Id: first.Id})
	if err != nil {
		t.Fatal(err)
	}
	rolled, ok := rollResp.(RollbackDataPolicyRevision201JSONResponse)
	if !ok || rolled.Reason != DataPolicyRevisionReasonRollback || rolled.SourceId == nil || *rolled.SourceId != first.Id {
		t.Fatalf("rollback = %+v", rollResp)
	}
	revResp, _ := f.h.ListDataPolicyRevisions(ctx, ListDataPolicyRevisionsRequestObject{})
	revisions := revResp.(ListDataPolicyRevisions200JSONResponse)
	if len(revisions) != 3 || revisions[0].Id != rolled.Id || !revisions[0].Published || revisions[1].Id != second.Id || revisions[1].Published {
		t.Fatalf("revisions = %+v", revisions)
	}

	// A rollback restores a catalog only while every engine that serves it
	// enforces data policies.
	if err := f.gdb.Model(&model.Engine{}).Where("id = ?", f.engineID.String()).Update("data_policies", false).Error; err != nil {
		t.Fatal(err)
	}
	rollResp, err = f.h.RollbackDataPolicyRevision(ctx, RollbackDataPolicyRevisionRequestObject{Id: first.Id})
	if err != nil {
		t.Fatal(err)
	}
	if refusedRoll, ok := rollResp.(RollbackDataPolicyRevision422JSONResponse); !ok || refusedRoll.Code != ErrorCodeDataPoliciesUnenforced {
		t.Fatalf("rollback over an unenforced binding = %+v", rollResp)
	}

	got := map[string]int{}
	for _, a := range f.audit.actions() {
		got[string(a)]++
	}
	if got["data_policy.publish"] != 3 || got["data_policy.rollback"] != 2 || got["data_policy.create"] != 1 {
		t.Fatalf("audit = %v", got)
	}
}

func TestDataPolicies_Status(t *testing.T) {
	t.Parallel()
	f := newDataPolicyFixture(t)
	status := func() DataPolicyStatus {
		t.Helper()
		resp, err := f.h.GetDataPolicyStatus(f.admin, GetDataPolicyStatusRequestObject{})
		if err != nil {
			t.Fatal(err)
		}
		out, ok := resp.(GetDataPolicyStatus200JSONResponse)
		if !ok {
			t.Fatalf("status = %+v", resp)
		}
		return DataPolicyStatus(out)
	}
	engineState := func(s DataPolicyStatus) DataPolicyEngineState {
		t.Helper()
		for _, e := range s.Engines {
			if e.EngineId == openapi_types.UUID(f.engineID.Bytes) {
				return e
			}
		}
		t.Fatalf("engine missing from %+v", s.Engines)
		return DataPolicyEngineState{}
	}
	s := status()
	if !s.Enabled || s.Published != nil || s.ExpectedPolicyDigest == nil {
		t.Fatalf("status = %+v", s)
	}
	if e := engineState(s); e.Health != DataPolicyHealthUnhealthy || e.Reason == nil || *e.Reason != datapolicy.UnhealthyNoReport {
		t.Fatalf("engine before any report = %+v", e)
	}
	if err := f.h.policies.RecordEngineStatus(context.Background(), datapolicy.EngineStatus{
		EngineID: uuid.UUID(f.engineID.Bytes), ReportedAt: time.Now(), ActiveRevision: "r1", PolicyDigest: *s.ExpectedPolicyDigest,
	}); err != nil {
		t.Fatal(err)
	}
	if e := engineState(status()); e.Health != DataPolicyHealthHealthy || e.ActiveRevision == nil || *e.ActiveRevision != "r1" {
		t.Fatalf("engine after a report = %+v", e)
	}
	if resp, _ := f.h.GetDataPolicyStatus(f.member, GetDataPolicyStatusRequestObject{}); resp == nil {
		t.Fatal("no response")
	} else if _, ok := resp.(GetDataPolicyStatus403JSONResponse); !ok {
		t.Fatalf("member status = %+v", resp)
	}
}

// TestDataPolicies_InspectViews reads a catalog's views through its Polaris
// connection: a DEFINER view comes back with its owner and what it reads,
// and a catalog bound through a view-capable connector whose views cannot be
// read is reported.
func TestDataPolicies_InspectViews(t *testing.T) {
	t.Parallel()
	f := newDataPolicyFixture(t)
	polaris := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/catalog/v1/oauth/tokens":
			_, _ = io.WriteString(w, `{"access_token":"t","token_type":"bearer","expires_in":3600}`)
		case r.URL.Path == "/api/catalog/v1/config":
			_, _ = io.WriteString(w, `{"defaults":{},"overrides":{"prefix":"wh"}}`)
		case r.URL.Path == "/api/catalog/v1/wh/namespaces" && r.URL.Query().Get("parent") == "":
			_, _ = io.WriteString(w, `{"namespaces":[["sales"]]}`)
		case r.URL.Path == "/api/catalog/v1/wh/namespaces":
			_, _ = io.WriteString(w, `{"namespaces":[]}`)
		case r.URL.Path == "/api/catalog/v1/wh/namespaces/sales/views":
			_, _ = io.WriteString(w, `{"identifiers":[{"namespace":["sales"],"name":"v_all"}]}`)
		case r.URL.Path == "/api/catalog/v1/wh/namespaces/sales/views/v_all":
			_, _ = io.WriteString(w, `{"metadata":{"current-version-id":1,"properties":{"trino.run-as-owner":"baleia_admin"},
				"versions":[{"version-id":1,"default-catalog":"lake","default-namespace":["sales"],
				"representations":[{"type":"sql","sql":"SELECT * FROM orders","dialect":"trino"}]}]}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(polaris.Close)

	kr, err := crypto.NewKeyring("data-policy-test-key-0123456789abcdef", "")
	if err != nil {
		t.Fatal(err)
	}
	f.h.keyring = kr
	var cat model.Catalog
	if err := f.gdb.Where("id = ?", f.catalog.String()).First(&cat).Error; err != nil {
		t.Fatal(err)
	}
	sealed, err := kr.Seal([]byte(`{"client_secret":"s"}`), catalogs.CredentialsAAD(repo.PGUUID(string(cat.ConnectionID))))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.gdb.Model(&model.CatalogConnection{}).Where("id = ?", string(cat.ConnectionID)).Updates(map[string]any{
		"url": polaris.URL, "config": model.JSON(`{"url":"` + polaris.URL + `","client_id":"c"}`), "credentials_enc": sealed,
	}).Error; err != nil {
		t.Fatal(err)
	}

	inspection, err := f.h.inspectViews(context.Background())
	if err != nil {
		t.Fatalf("inspectViews: %v", err)
	}
	if len(inspection.Uninspectable) != 0 || len(inspection.Views) != 1 {
		t.Fatalf("inspection = %+v", inspection)
	}
	v := inspection.Views[0]
	if v.View.Owner != "baleia_admin" || len(v.Reads) != 1 || v.Reads[0] != datapolicy.NewTableName("lake", "sales", "orders") {
		t.Fatalf("view = %+v", v)
	}

	// A Hive catalog bound on the same engine has views Baleia cannot read.
	var source model.CatalogConnection
	if err := f.gdb.Where("id = ?", string(cat.ConnectionID)).First(&source).Error; err != nil {
		t.Fatal(err)
	}
	hive, err := repo.New(f.gdb).CreateCatalogConnection(context.Background(), db.CreateCatalogConnectionParams{
		ID: repo.PGUUID(repo.NewUUID()), Name: "policies-hive", Type: "hive", Url: "thrift://hms.test:9083",
		CreatedBy: repo.PGUUID(string(*source.CreatedBy)),
	})
	if err != nil {
		t.Fatal(err)
	}
	hms, warehouse, hiveConnector := model.Text("hms"), "hms", model.Text("hive")
	hiveCatalog := model.Catalog{
		ID: model.UUID(repo.NewUUID()), ConnectionID: model.UUID(repo.UUIDString(hive.ID)), Name: "hms",
		TrinoCatalogName: &hms, ClusterID: cat.ClusterID, Warehouse: &warehouse, Connector: &hiveConnector, Origin: "source",
	}
	if err := f.gdb.Create(&hiveCatalog).Error; err != nil {
		t.Fatal(err)
	}
	bindingConnector := "hive"
	if err := f.gdb.Create(&model.CatalogBinding{
		ID: model.UUID(repo.NewUUID()), CatalogID: hiveCatalog.ID, EngineID: model.UUID(f.engineID.String()),
		EngineCatalogName: "hms", Connector: &bindingConnector, Mode: "managed", Enabled: true, LifecycleState: "active",
	}).Error; err != nil {
		t.Fatal(err)
	}
	inspection, err = f.h.inspectViews(context.Background())
	if err != nil {
		t.Fatalf("inspectViews with a Hive catalog: %v", err)
	}
	if len(inspection.Uninspectable) != 1 || inspection.Uninspectable[0] != "hms" || len(inspection.Views) != 1 {
		t.Fatalf("inspection with a Hive catalog = %+v", inspection)
	}
}
