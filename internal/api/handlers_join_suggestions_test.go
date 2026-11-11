package api

import (
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/store/model"
	"github.com/galileostudio/baleia/internal/store/repo"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// addCustomers adds a customers table to the fixture's granted catalog (B1,
// #554).
func addCustomers(t *testing.T, f *metaFixture) openapi_types.UUID {
	t.Helper()
	now := time.Now()
	table := model.MetadataTable{
		ID: model.UUID(repo.NewUUID()), CatalogID: model.UUID(repo.UUIDString(pgUUIDOf(f.lake))),
		Namespace: model.StringArray{"sales"}, Name: "customers", Kind: "table", Fingerprint: "f",
		LastSeenAt: now, LastChangedAt: now,
	}
	if err := f.gdb.Create(&table).Error; err != nil {
		t.Fatal(err)
	}
	return openapi_types.UUID(repo.PGUUID(string(table.ID)).Bytes)
}

// addJoinHistory records one finished statement that joined orders (o) and
// customers (c) on o.<left> = c.<right> (B1, #554).
func addJoinHistory(t *testing.T, f *metaFixture, leftColumn, rightColumn string) {
	t.Helper()
	now := time.Now()
	execution := model.QueryExecution{
		ID: model.UUID(repo.NewUUID()), WorkspaceID: model.UUID(f.ws.String()),
		UserID: model.UUID(f.owner), Status: "finished", SQL: "select 1",
		StartedAt: now, FinishedAt: &now,
	}
	if err := f.gdb.Create(&execution).Error; err != nil {
		t.Fatal(err)
	}
	statement := model.QueryStatement{
		ID: model.UUID(repo.NewUUID()), ExecutionID: execution.ID, ExecutionStartedAt: execution.StartedAt,
		Position: 1, SQL: "select 1", Status: "finished", StartedAt: now, FinishedAt: &now,
		ResultSets: model.JSON(`[]`),
	}
	if err := f.gdb.Create(&statement).Error; err != nil {
		t.Fatal(err)
	}
	workspace := model.UUID(f.ws.String())
	catalog := model.UUID(repo.UUIDString(pgUUIDOf(f.lake)))
	for _, ref := range []struct {
		table model.Text
		alias model.Text
	}{{"orders", "o"}, {"customers", "c"}} {
		if err := f.gdb.Create(&model.StatementReference{
			StatementID: statement.ID, ExecutionStartedAt: statement.ExecutionStartedAt,
			WorkspaceID: workspace, CatalogID: &catalog,
			Namespace: "sales", TableNameValue: ref.table, Alias: ref.alias, Kind: "read",
		}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := f.gdb.Create(&model.StatementJoin{
		StatementID: statement.ID, ExecutionStartedAt: statement.ExecutionStartedAt,
		WorkspaceID: workspace, LeftAlias: "o", LeftColumn: model.Text(leftColumn),
		RightAlias: "c", RightColumn: model.Text(rightColumn),
	}).Error; err != nil {
		t.Fatal(err)
	}
}

// addForeignKey records orders.customer_id -> customers.id.
func addForeignKey(t *testing.T, f *metaFixture) {
	t.Helper()
	catalog := model.UUID(repo.UUIDString(pgUUIDOf(f.lake)))
	if err := f.gdb.Create(&model.MetadataForeignKey{
		CatalogID: catalog, TableID: model.UUID(repo.UUIDString(pgUUIDOf(f.orders))),
		ColumnName: "customer_id", RefNamespace: model.StringArray{"sales"},
		RefTable: "customers", RefColumn: "id", LastSeenAt: time.Now(),
	}).Error; err != nil {
		t.Fatal(err)
	}
}

func TestJoinSuggestions_HistoryAndForeignKey(t *testing.T) {
	t.Parallel()
	f := newMetaFixture(t)
	customers := addCustomers(t, f)
	addJoinHistory(t, f, "customer_id", "id")
	addForeignKey(t, f)

	resp, err := f.h.ListJoinSuggestions(f.as(f.owner), ListJoinSuggestionsRequestObject{
		Id:     f.ws,
		Params: ListJoinSuggestionsParams{LeftTable: f.orders, RightTable: customers},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, ok := resp.(ListJoinSuggestions200JSONResponse)
	if !ok {
		t.Fatalf("response = %T", resp)
	}
	if len(got) != 1 {
		t.Fatalf("suggestions = %+v, want one (history and the key dedupe)", got)
	}
	s := got[0]
	if s.LeftColumn != "customer_id" || s.RightColumn != "id" || s.UseCount != 1 || s.Source != History {
		t.Fatalf("suggestion = %+v", s)
	}
	if s.LastUsedAt == nil || s.Score == nil || *s.Score <= 0 {
		t.Fatalf("suggestion is missing its recency and score: %+v", s)
	}
}

func TestJoinSuggestions_ForeignKeyOnly(t *testing.T) {
	t.Parallel()
	f := newMetaFixture(t)
	customers := addCustomers(t, f)
	addForeignKey(t, f)

	resp, err := f.h.ListJoinSuggestions(f.as(f.owner), ListJoinSuggestionsRequestObject{
		Id:     f.ws,
		Params: ListJoinSuggestionsParams{LeftTable: f.orders, RightTable: customers},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := resp.(ListJoinSuggestions200JSONResponse)
	if len(got) != 1 || got[0].Source != ForeignKey || got[0].LeftColumn != "customer_id" || got[0].RightColumn != "id" {
		t.Fatalf("suggestions = %+v", got)
	}
}

// TestJoinSuggestions_Scoped: another workspace's history and a table of an
// ungranted catalog are invisible (B1, #554).
func TestJoinSuggestions_Scoped(t *testing.T) {
	t.Parallel()
	f := newMetaFixture(t)
	customers := addCustomers(t, f)
	addJoinHistory(t, f, "customer_id", "id")

	// The same pair, asked from the stranger's workspace: 404 (not granted).
	resp, err := f.h.ListJoinSuggestions(f.as(f.stranger), ListJoinSuggestionsRequestObject{
		Id:     f.strangerWS,
		Params: ListJoinSuggestionsParams{LeftTable: f.orders, RightTable: customers},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := resp.(ListJoinSuggestions404JSONResponse); !ok {
		t.Fatalf("foreign workspace response = %T, want 404", resp)
	}

	// A table of a catalog not granted to the workspace is a 404.
	resp, err = f.h.ListJoinSuggestions(f.as(f.owner), ListJoinSuggestionsRequestObject{
		Id:     f.ws,
		Params: ListJoinSuggestionsParams{LeftTable: f.otherTable, RightTable: customers},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := resp.(ListJoinSuggestions404JSONResponse); !ok {
		t.Fatalf("ungranted table response = %T, want 404", resp)
	}
}
