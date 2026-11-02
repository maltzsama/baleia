package api

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// addCustomers adds a customers table to the fixture's granted catalog (B1,
// #554).
func addCustomers(t *testing.T, f *metaFixture) openapi_types.UUID {
	t.Helper()
	var id pgtype.UUID
	if err := f.pool.QueryRow(context.Background(),
		`INSERT INTO metadata_tables (catalog_id, namespace, name, kind, fingerprint, last_seen_at, last_changed_at)
		 VALUES ($1, '{sales}', 'customers', 'table', 'f', now(), now()) RETURNING id`, f.lake).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return openapi_types.UUID(id.Bytes)
}

// addJoinHistory records one finished statement that joined orders (o) and
// customers (c) on o.<left> = c.<right> (B1, #554).
func addJoinHistory(t *testing.T, f *metaFixture, leftColumn, rightColumn string) {
	t.Helper()
	ctx := context.Background()
	var exec, stmt pgtype.UUID
	if err := f.pool.QueryRow(ctx,
		`INSERT INTO query_executions (workspace_id, user_id, status, sql, started_at, finished_at)
		 VALUES ($1, $2, 'finished', 'select 1', now(), now()) RETURNING id`,
		f.ws, f.owner).Scan(&exec); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(ctx,
		`INSERT INTO query_statements (execution_id, position, sql, status, started_at, finished_at)
		 VALUES ($1, 1, 'select 1', 'finished', now(), now()) RETURNING id`, exec).Scan(&stmt); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx,
		`INSERT INTO statement_references (statement_id, workspace_id, catalog_id, namespace, table_name, alias, kind)
		 VALUES ($1, $2, $3, 'sales', 'orders', 'o', 'read'),
		        ($1, $2, $3, 'sales', 'customers', 'c', 'read')`, stmt, f.ws, f.lake); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx,
		`INSERT INTO statement_joins (statement_id, workspace_id, left_alias, left_column, right_alias, right_column)
		 VALUES ($1, $2, 'o', $3, 'c', $4)`, stmt, f.ws, leftColumn, rightColumn); err != nil {
		t.Fatal(err)
	}
}

func TestJoinSuggestions_HistoryAndForeignKey(t *testing.T) {
	t.Parallel()
	f := newMetaFixture(t)
	customers := addCustomers(t, f)
	addJoinHistory(t, f, "customer_id", "id")
	if _, err := f.pool.Exec(context.Background(),
		`INSERT INTO metadata_foreign_keys (catalog_id, table_id, column_name, ref_namespace, ref_table, ref_column)
		 VALUES ($1, $2, 'customer_id', '{sales}', 'customers', 'id')`, f.lake, f.orders); err != nil {
		t.Fatal(err)
	}

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
	if _, err := f.pool.Exec(context.Background(),
		`INSERT INTO metadata_foreign_keys (catalog_id, table_id, column_name, ref_namespace, ref_table, ref_column)
		 VALUES ($1, $2, 'customer_id', '{sales}', 'customers', 'id')`, f.lake, f.orders); err != nil {
		t.Fatal(err)
	}

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
