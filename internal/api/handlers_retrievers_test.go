package api

import (
	"context"
	"testing"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/jackc/pgx/v5/pgtype"
)

// fakeRetrieverQuerier supports CreateRetriever by giving the workspace an
// enabled Iceberg catalog and capturing creation.
type fakeRetrieverQuerier struct {
	db.Querier
	created *db.CreateRetrieverParams
}

func (f *fakeRetrieverQuerier) CheckWorkspaceAccess(context.Context, db.CheckWorkspaceAccessParams) (bool, error) {
	return true, nil
}

func (f *fakeRetrieverQuerier) GetWorkspaceByID(_ context.Context, id pgtype.UUID) (db.Workspace, error) {
	return db.Workspace{ID: id, OwnerUserID: pgtype.UUID{Bytes: [16]byte{1}, Valid: true}}, nil
}

func (f *fakeRetrieverQuerier) ListCatalogConnections(context.Context, pgtype.UUID) ([]db.CatalogConnection, error) {
	return []db.CatalogConnection{{ID: pgtype.UUID{Bytes: [16]byte{2}, Valid: true}, Type: "polaris"}}, nil
}

func (f *fakeRetrieverQuerier) ListCatalogsByConnection(context.Context, pgtype.UUID) ([]db.Catalog, error) {
	return []db.Catalog{
		{Name: "tpch", TrinoCatalogName: strp("tpch"), Enabled: true},
	}, nil
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
			DestCatalog:   "tpch",
			DestNamespace: "sf1",
			DestTable:     "daily_snapshot",
			CronSchedule:  "0 */6 * * *",
			RefreshType:   &full,
		},
	}
}

func TestCreateRetriever_RequiresDestination(t *testing.T) {
	cases := []struct {
		name string
		set  func(*CreateRetrieverRequest)
	}{
		{"empty dest_catalog", func(r *CreateRetrieverRequest) { r.DestCatalog = "" }},
		{"empty dest_namespace", func(r *CreateRetrieverRequest) { r.DestNamespace = "" }},
		{"empty dest_table", func(r *CreateRetrieverRequest) { r.DestTable = "" }},
		{"bad dest_identifier", func(r *CreateRetrieverRequest) { r.DestTable = "with-hyphen" }},
		{"bad cron", func(r *CreateRetrieverRequest) { r.CronSchedule = "not a cron" }},
	}
	for _, c := range cases {
		req := retrieverRequest()
		c.set(req.Body)

		resp, err := (&Handlers{queries: &fakeRetrieverQuerier{}}).CreateRetriever(testContext(), req)
		if err != nil {
			t.Fatalf("[%s] unexpected error: %v", c.name, err)
		}
		if _, ok := resp.(CreateRetriever422JSONResponse); !ok {
			t.Fatalf("[%s] want 422, got %T", c.name, resp)
		}
	}
}

func TestCreateRetriever_RejectsMultiStatementAndDDL(t *testing.T) {
	for name, def := range map[string]string{
		"multi":       "SELECT 1; SELECT 2",
		"catalog_ddl": "CREATE CATALOG x USING tpch",
		"not_select":  "INSERT INTO t VALUES (1)",
	} {
		req := retrieverRequest()
		req.Body.DefinitionSql = def

		resp, err := (&Handlers{queries: &fakeRetrieverQuerier{}}).CreateRetriever(testContext(), req)
		if err != nil {
			t.Fatalf("[%s] unexpected error: %v", name, err)
		}
		if _, ok := resp.(CreateRetriever422JSONResponse); !ok {
			t.Fatalf("[%s] want 422, got %T", name, resp)
		}
	}
}

func TestCreateRetriever_IncrementalNotSupported(t *testing.T) {
	req := retrieverRequest()
	inc := CreateRetrieverRequestRefreshType("incremental")
	req.Body.RefreshType = &inc

	resp, err := (&Handlers{queries: &fakeRetrieverQuerier{}}).CreateRetriever(testContext(), req)
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
	fake := &fakeRetrieverQuerier{}
	resp, err := (&Handlers{queries: fake}).CreateRetriever(testContext(), retrieverRequest())
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
	if fake.created.RefreshType != "full" {
		t.Fatalf("refresh type = %q, want full (default)", fake.created.RefreshType)
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
	req := DeleteRetrieverRequestObject{Id: PathId{0xAA}, RetrieverId: [16]byte{0xBB}}

	q := &fakeRetrieverDeleter{rows: 1}
	resp, err := (&Handlers{queries: q}).DeleteRetriever(testContext(), req)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := resp.(DeleteRetriever204Response); !ok {
		t.Fatalf("want 204, got %T", resp)
	}
	if q.deleted == nil || q.deleted.ID.Bytes != [16]byte{0xBB} || q.deleted.WorkspaceID.Bytes != [16]byte{0xAA} {
		t.Fatalf("deleted %+v, want the retriever in the request's workspace", q.deleted)
	}

	resp, err = (&Handlers{queries: &fakeRetrieverDeleter{rows: 0}}).DeleteRetriever(testContext(), req)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := resp.(DeleteRetriever404JSONResponse); !ok {
		t.Fatalf("missing retriever: want 404, got %T", resp)
	}
}
