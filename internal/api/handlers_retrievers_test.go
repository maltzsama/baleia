package api

import (
	"context"
	"testing"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/dialect"
)

// fakeRetrieverQuerier supports CreateRetriever by giving the workspace an
// enabled Iceberg catalog and capturing creation.
type fakeRetrieverQuerier struct {
	db.Querier
	created *db.CreateRetrieverParams
	// role is the caller's workspace role (#653); empty means admin.
	// Retriever mutations need an editor (#613, #653).
	role string
}

func (f *fakeRetrieverQuerier) IsWorkspaceOwner(context.Context, db.IsWorkspaceOwnerParams) (bool, error) {
	return f.role == "" || f.role == "admin", nil
}

func (f *fakeRetrieverQuerier) GetWorkspaceMemberRole(context.Context, db.GetWorkspaceMemberRoleParams) (string, error) {
	if f.role == "" {
		return "admin", nil
	}
	return f.role, nil
}

func (f *fakeRetrieverQuerier) CheckWorkspaceAccess(context.Context, db.CheckWorkspaceAccessParams) (bool, error) {
	return true, nil
}

func (f *fakeRetrieverQuerier) GetWorkspaceByID(_ context.Context, id db.UUID) (db.Workspace, error) {
	return db.Workspace{ID: id, OwnerUserID: db.UUID{Bytes: [16]byte{1}, Valid: true}}, nil
}

func (f *fakeRetrieverQuerier) ListCatalogConnections(context.Context, db.UUID) ([]db.CatalogConnection, error) {
	return []db.CatalogConnection{{ID: db.UUID{Bytes: [16]byte{2}, Valid: true}, Type: "polaris"}}, nil
}

func (f *fakeRetrieverQuerier) ListCatalogsByConnection(context.Context, db.UUID) ([]db.Catalog, error) {
	return []db.Catalog{
		{Name: "tpch", TrinoCatalogName: strp("tpch"), Enabled: true},
	}, nil
}

func (f *fakeRetrieverQuerier) ListRetrieverDestinations(context.Context, db.UUID) ([]db.ListRetrieverDestinationsRow, error) {
	return []db.ListRetrieverDestinationsRow{{
		BindingID:         db.UUID{Bytes: [16]byte{3}, Valid: true},
		EngineCatalogName: "tpch",
		Connector:         "iceberg",
		CatalogID:         db.UUID{Bytes: [16]byte{2}, Valid: true},
		CatalogName:       "tpch",
		EngineID:          db.UUID{Bytes: [16]byte{4}, Valid: true},
		EngineName:        "trino",
		EngineKind:        "trino",
	}}, nil
}

func (f *fakeRetrieverQuerier) LatestRetrieverRefresh(context.Context, db.UUID) (db.LatestRetrieverRefreshRow, error) {
	return db.LatestRetrieverRefreshRow{}, db.ErrNoRows
}

func (f *fakeRetrieverQuerier) CreateRetriever(_ context.Context, arg db.CreateRetrieverParams) (db.Retriever, error) {
	f.created = &arg
	return db.Retriever{}, nil
}

func retrieverRequest() CreateRetrieverRequestObject {
	full := CreateRetrieverRequestRefreshType("full")
	return CreateRetrieverRequestObject{
		Id: PathId{0xAA},
		Body: &CreateRetrieverJSONRequestBody{
			Name:          "daily",
			DefinitionSql: "SELECT 1 AS x",
			DestCatalog:   strp("tpch"),
			DestNamespace: "sf1",
			DestTable:     "daily_snapshot",
			CronSchedule:  "0 */6 * * *",
			RefreshType:   &full,
		},
	}
}

func TestCreateRetriever_RequiresDestination(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		set  func(*CreateRetrieverRequest)
	}{
		{"empty dest_catalog", func(r *CreateRetrieverRequest) { r.DestCatalog = nil }},
		{"empty dest_namespace", func(r *CreateRetrieverRequest) { r.DestNamespace = "" }},
		{"empty dest_table", func(r *CreateRetrieverRequest) { r.DestTable = "" }},
		{"bad dest_identifier", func(r *CreateRetrieverRequest) { r.DestTable = "with-hyphen" }},
		{"bad cron", func(r *CreateRetrieverRequest) { r.CronSchedule = "not a cron" }},
	}
	for _, c := range cases {
		req := retrieverRequest()
		c.set(req.Body)

		resp, err := (testAPI(handlerFields{dialects: dialect.NewRegistry(), queries: &fakeRetrieverQuerier{}})).CreateRetriever(testContext(), req)
		if err != nil {
			t.Fatalf("[%s] unexpected error: %v", c.name, err)
		}
		if _, ok := resp.(CreateRetriever422JSONResponse); !ok {
			t.Fatalf("[%s] want 422, got %T", c.name, resp)
		}
	}
}

func TestCreateRetriever_RejectsMultiStatementAndDDL(t *testing.T) {
	t.Parallel()
	for name, def := range map[string]string{
		"multi":       "SELECT 1; SELECT 2",
		"catalog_ddl": "CREATE CATALOG x USING tpch",
		"not_select":  "INSERT INTO t VALUES (1)",
	} {
		req := retrieverRequest()
		req.Body.DefinitionSql = def

		resp, err := (testAPI(handlerFields{dialects: dialect.NewRegistry(), queries: &fakeRetrieverQuerier{}})).CreateRetriever(testContext(), req)
		if err != nil {
			t.Fatalf("[%s] unexpected error: %v", name, err)
		}
		if _, ok := resp.(CreateRetriever422JSONResponse); !ok {
			t.Fatalf("[%s] want 422, got %T", name, resp)
		}
	}
}

func TestCreateRetriever_IncrementalNotSupported(t *testing.T) {
	t.Parallel()
	req := retrieverRequest()
	inc := CreateRetrieverRequestRefreshType("incremental")
	req.Body.RefreshType = &inc

	resp, err := (testAPI(handlerFields{dialects: dialect.NewRegistry(), queries: &fakeRetrieverQuerier{}})).CreateRetriever(testContext(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, ok := resp.(CreateRetriever422JSONResponse)
	if !ok {
		t.Fatalf("want 422, got %T", resp)
	}
	if got.Code != "not_supported" {
		t.Fatalf("code = %q, want not_supported", got.Code)
	}
}

func TestCreateRetriever_StoresDestinationFields(t *testing.T) {
	t.Parallel()
	fake := &fakeRetrieverQuerier{}
	resp, err := (testAPI(handlerFields{dialects: dialect.NewRegistry(), queries: fake, tx: directTx{q: fake}})).CreateRetriever(testContext(), retrieverRequest())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := resp.(CreateRetriever201JSONResponse); !ok {
		t.Fatalf("want 201, got %T", resp)
	}
	if fake.created == nil {
		t.Fatal("CreateRetriever was not called")
	}
	if fake.created.DestCatalog != "tpch" || fake.created.DestNamespace != "sf1" || fake.created.DestTable != "daily_snapshot" {
		t.Fatalf("destination not stored: %+v", fake.created)
	}
	if !fake.created.DestBindingID.Valid {
		t.Fatal("dest_binding_id was not stored")
	}
	if fake.created.RefreshType != "full" {
		t.Fatalf("refresh type = %q, want full (default)", fake.created.RefreshType)
	}
}

// TestCreateRetriever_EditorsOnly: a workspace viewer may not create a
// retriever even though the workspace has a writable destination; an editor
// may (#613, #653).
func TestCreateRetriever_EditorsOnly(t *testing.T) {
	t.Parallel()
	editor := &fakeRetrieverQuerier{role: "editor"}
	if resp, err := (testAPI(handlerFields{dialects: dialect.NewRegistry(), queries: editor})).CreateRetriever(testContext(), retrieverRequest()); err != nil {
		t.Fatal(err)
	} else if _, forbidden := resp.(CreateRetriever403JSONResponse); forbidden || editor.created == nil {
		t.Fatalf("an editor may create a retriever, got %T", resp)
	}
	fake := &fakeRetrieverQuerier{role: "viewer"}
	resp, err := (testAPI(handlerFields{dialects: dialect.NewRegistry(), queries: fake})).CreateRetriever(testContext(), retrieverRequest())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, ok := resp.(CreateRetriever403JSONResponse); !ok || got.Code != ErrorCodeForbidden {
		t.Fatalf("want 403 forbidden, got %T %+v", resp, resp)
	}
	if fake.created != nil {
		t.Fatal("a viewer created a retriever")
	}
}

type fakeRetrieverDeleter struct {
	fakeRetrieverQuerier
	deleted *db.DeleteRetrieverParams
	rows    int64
}

func (f *fakeRetrieverDeleter) DeleteRetriever(_ context.Context, arg db.DeleteRetrieverParams) (int64, error) {
	f.deleted = &arg
	return f.rows, nil
}

// TestDeleteRetriever: the row is deleted in the caller's workspace; one that
// is not there is 404.
func TestDeleteRetriever(t *testing.T) {
	t.Parallel()
	req := DeleteRetrieverRequestObject{Id: PathId{0xAA}, RetrieverId: [16]byte{0xBB}}

	q := &fakeRetrieverDeleter{rows: 1}
	resp, err := (testAPI(handlerFields{dialects: dialect.NewRegistry(), queries: q, tx: directTx{q: q}})).DeleteRetriever(testContext(), req)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := resp.(DeleteRetriever204Response); !ok {
		t.Fatalf("want 204, got %T", resp)
	}
	if q.deleted == nil || q.deleted.ID.Bytes != [16]byte{0xBB} || q.deleted.WorkspaceID.Bytes != [16]byte{0xAA} {
		t.Fatalf("deleted %+v, want the retriever in the request's workspace", q.deleted)
	}

	resp, err = (testAPI(handlerFields{dialects: dialect.NewRegistry(), queries: &fakeRetrieverDeleter{rows: 0}})).DeleteRetriever(testContext(), req)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := resp.(DeleteRetriever404JSONResponse); !ok {
		t.Fatalf("missing retriever: want 404, got %T", resp)
	}
}
