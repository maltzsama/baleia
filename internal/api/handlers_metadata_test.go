package api

import (
	"context"
	"testing"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/temporal/workflows"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
)

// metadataTemporal records the workflow a metadata sync starts.
type metadataTemporal struct {
	client.Client
	opts  client.StartWorkflowOptions
	input workflows.MetadataSyncInput
	err   error
}

func (f *metadataTemporal) ExecuteWorkflow(_ context.Context, o client.StartWorkflowOptions, _ interface{}, args ...interface{}) (client.WorkflowRun, error) {
	f.opts = o
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
