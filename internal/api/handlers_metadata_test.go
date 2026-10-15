package api

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/connections"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/temporal/workflows"
	"github.com/jackc/pgx/v5/pgtype"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
)

// metadataTemporal records the workflows a metadata sync starts.
type metadataTemporal struct {
	client.Client
	opts   client.StartWorkflowOptions
	input  workflows.MetadataSyncInput
	starts []client.StartWorkflowOptions
	err    error
}

func (f *metadataTemporal) ExecuteWorkflow(_ context.Context, o client.StartWorkflowOptions, _ interface{}, args ...interface{}) (client.WorkflowRun, error) {
	f.opts = o
	f.starts = append(f.starts, o)
	if len(args) == 1 {
		f.input, _ = args[0].(workflows.MetadataSyncInput)
	}
	return nil, f.err
}

// TestSyncCatalogMetadata: the owner and admins start one pass per catalog
// (the workflow id is the catalog's); a running pass is a 409; strangers see
// 404.
func TestSyncCatalogMetadata(t *testing.T) {
	h, q, _ := newAuthzHandlers(t)
	_, cat := reqIDs(q)
	tc := &metadataTemporal{}
	h.temporalClient = tc

	sync := func(ctx context.Context) int {
		resp, err := h.SyncCatalogMetadata(ctx, SyncCatalogMetadataRequestObject{CatalogId: cat})
		if err != nil {
			t.Fatal(err)
		}
		return visitCode(t, resp)
	}
	if got := sync(ctxAs(ownerID, "member")); got != 202 {
		t.Fatalf("owner: %d", got)
	}
	catalogID := uuidString(q.cat.ID)
	if tc.opts.ID != "metadata-sync-"+catalogID || !tc.opts.WorkflowExecutionErrorWhenAlreadyStarted || tc.input.CatalogID != catalogID {
		t.Fatalf("workflow options %+v input %+v", tc.opts, tc.input)
	}
	if got := sync(ctxAs(adminID, "admin")); got != 202 {
		t.Fatalf("admin: %d", got)
	}
	if got := sync(ctxAs(strangerID, "member")); got != 404 {
		t.Fatalf("stranger: %d", got)
	}
	tc.err = serviceerror.NewWorkflowExecutionAlreadyStarted("running", "", "")
	if got := sync(ctxAs(ownerID, "member")); got != 409 {
		t.Fatalf("running pass: %d", got)
	}
}

// runsFake answers the runs query on top of the catalog authz fake.
type runsFake struct {
	*authzFake
	limit int32
}

func (q *runsFake) ListMetadataSyncRuns(_ context.Context, p db.ListMetadataSyncRunsParams) ([]db.ListMetadataSyncRunsRow, error) {
	q.limit = p.Limit
	code := "engine_unavailable"
	return []db.ListMetadataSyncRunsRow{{ID: q.cat.ID, CatalogID: q.cat.ID, Status: "failed", SourcePath: "engine", ErrorCode: &code}}, nil
}

func TestListCatalogMetadataSyncRuns(t *testing.T) {
	h, q, _ := newAuthzHandlers(t)
	rf := &runsFake{authzFake: q}
	h.queries = rf
	_, cat := reqIDs(q)
	resp, err := h.ListCatalogMetadataSyncRuns(ctxAs(ownerID, "member"), ListCatalogMetadataSyncRunsRequestObject{CatalogId: cat})
	if err != nil {
		t.Fatal(err)
	}
	runs, ok := resp.(ListCatalogMetadataSyncRuns200JSONResponse)
	if !ok || len(runs) != 1 || *runs[0].ErrorCode != "engine_unavailable" || runs[0].SourcePath != "engine" || rf.limit != 20 {
		t.Fatalf("resp %+v limit %d", resp, rf.limit)
	}
	resp, err = h.ListCatalogMetadataSyncRuns(ctxAs(strangerID, "member"), ListCatalogMetadataSyncRunsRequestObject{CatalogId: cat})
	if err != nil {
		t.Fatal(err)
	}
	if got := visitCode(t, resp); got != 404 {
		t.Fatalf("stranger: %d", got)
	}
}

// TestUpdateCatalog_StartsSync: a changed catalog gets a metadata pass right
// away, reusing a pass already running (#17).
func TestUpdateCatalog_StartsSync(t *testing.T) {
	h, q, _ := newAuthzHandlers(t)
	tc := &metadataTemporal{}
	h.temporalClient = tc
	conn, cat := reqIDs(q)
	resp, err := h.UpdateCatalog(ctxAs(ownerID, "member"), UpdateCatalogRequestObject{ConnectionId: conn, CatalogId: cat, Body: &CreateCatalogRequest{Warehouse: "wh2"}})
	if err != nil {
		t.Fatal(err)
	}
	if code := visitCode(t, resp); code != 200 {
		t.Fatalf("status %d", code)
	}
	if len(tc.starts) != 1 || tc.starts[0].ID != "metadata-sync-"+uuidString(q.cat.ID) ||
		tc.starts[0].WorkflowIDConflictPolicy != enumspb.WORKFLOW_ID_CONFLICT_POLICY_USE_EXISTING {
		t.Fatalf("starts = %+v", tc.starts)
	}

	// A Temporal failure never fails the request.
	tc.err = errors.New("temporal down")
	resp, err = h.UpdateCatalog(ctxAs(ownerID, "member"), UpdateCatalogRequestObject{ConnectionId: conn, CatalogId: cat, Body: &CreateCatalogRequest{Warehouse: "wh3"}})
	if err != nil || visitCode(t, resp) != 200 {
		t.Fatalf("update with temporal down: %v", err)
	}
}

// TestCreateDirectEngine_StartsMetadataSyncs: every native catalog of a new
// engine gets a metadata pass right away.
func TestCreateDirectEngine_StartsMetadataSyncs(t *testing.T) {
	q := &engineQuerier{conn: ownedConnection(t, "clickhouse", testUserID)}
	p := &fakeEngineProvider{kind: "clickhouse", roles: []connections.Role{connections.RoleMetadata, connections.RoleDirect}, catalogs: []string{"default", "logs"}}
	h := engineHandlers(t, q, p)
	tc := &metadataTemporal{}
	h.temporalClient = tc
	if rec := createEngine(t, h, "ch"); rec.Code != 201 {
		t.Fatalf("response = %d %s", rec.Code, rec.Body)
	}
	if len(tc.starts) != 2 {
		t.Fatalf("starts = %+v", tc.starts)
	}
}

// TestCatalog_FreshnessFields: a catalog shows its latest pass: never before
// the first, then the end of the latest finished one and a failed pass's
// code.
func TestCatalog_FreshnessFields(t *testing.T) {
	h, q, _ := newAuthzHandlers(t)
	conn, cat := reqIDs(q)
	get := func() Catalog {
		resp, err := h.GetCatalog(ctxAs(ownerID, "member"), GetCatalogRequestObject{ConnectionId: conn, CatalogId: cat})
		if err != nil {
			t.Fatal(err)
		}
		c, ok := resp.(GetCatalog200JSONResponse)
		if !ok {
			t.Fatalf("response %T", resp)
		}
		return Catalog(c)
	}
	c := get()
	if c.MetadataSyncStatus == nil || *c.MetadataSyncStatus != CatalogMetadataSyncStatusNever || c.MetadataSyncedAt != nil || c.MetadataSyncErrorCode != nil {
		t.Fatalf("before any pass: %+v %+v %+v", c.MetadataSyncStatus, c.MetadataSyncedAt, c.MetadataSyncErrorCode)
	}
	at := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	q.syncState = db.GetCatalogSyncStateRow{LastStatus: "failed", LastErrorCode: "engine_unavailable", SyncedAt: pgtype.Timestamptz{Time: at, Valid: true}}
	c = get()
	if *c.MetadataSyncStatus != CatalogMetadataSyncStatusFailed || !c.MetadataSyncedAt.Equal(at) || *c.MetadataSyncErrorCode != "engine_unavailable" {
		t.Fatalf("after a failed pass: %+v %+v %+v", *c.MetadataSyncStatus, c.MetadataSyncedAt, c.MetadataSyncErrorCode)
	}
}
