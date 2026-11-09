package api

import (
	"context"
	"testing"

	"github.com/galileostudio/baleia/internal/store/repo"
	"github.com/galileostudio/baleia/internal/testdb"
	"github.com/jackc/pgx/v5/pgtype"
)

// TestPopularTables_Refresh counts two finished statements that read orders,
// then lists the table with its count and excludes an ungranted catalog (B7,
// #562).
func TestPopularTables_Refresh(t *testing.T) {
	t.Parallel()
	f := newMetaFixture(t)
	ctx := context.Background()
	for range 2 {
		var exec, stmt pgtype.UUID
		if err := f.pool.QueryRow(ctx, `
			INSERT INTO query_executions (workspace_id, user_id, status, sql, started_at, finished_at)
			VALUES ($1, $2, 'finished', 'select 1', now(), now()) RETURNING id`, f.ws, f.owner).Scan(&exec); err != nil {
			t.Fatal(err)
		}
		if err := f.pool.QueryRow(ctx, `
			INSERT INTO query_statements (execution_id, position, sql, status, started_at, finished_at)
			VALUES ($1, 1, 'select 1', 'finished', now(), now()) RETURNING id`, exec).Scan(&stmt); err != nil {
			t.Fatal(err)
		}
		if _, err := f.pool.Exec(ctx, `
			INSERT INTO statement_references (statement_id, workspace_id, catalog_id, namespace, table_name, alias, kind)
			VALUES ($1, $2, $3, 'sales', 'orders', 'o', 'read')`, stmt, f.ws, f.lake); err != nil {
			t.Fatal(err)
		}
	}
	q := repo.New(testdb.GORM(t, f.pool))
	if err := q.DeleteTablePopularity(ctx); err != nil {
		t.Fatal(err)
	}
	if err := q.InsertTablePopularity(ctx); err != nil {
		t.Fatal(err)
	}

	resp, err := f.h.ListPopularTables(f.as(f.owner), ListPopularTablesRequestObject{Id: f.ws})
	if err != nil {
		t.Fatal(err)
	}
	got, ok := resp.(ListPopularTables200JSONResponse)
	if !ok {
		t.Fatalf("response = %T", resp)
	}
	if len(got) != 1 || got[0].TableId != f.orders || got[0].Uses != 2 {
		t.Fatalf("popular tables = %+v", got)
	}
	if got[0].Score <= 0 || got[0].LastUsedAt.IsZero() {
		t.Fatalf("popular table without a score or recency: %+v", got[0])
	}

	// A table of a catalog the workspace may not use never appears.
	if _, err := f.pool.Exec(ctx, `
		INSERT INTO table_popularity (workspace_id, catalog_id, namespace, table_name, uses, score, last_used_at)
		VALUES ($1, $2, 'sales', 'orders_secret', 9, 9, now())`, f.ws, f.other); err != nil {
		t.Fatal(err)
	}
	resp, err = f.h.ListPopularTables(f.as(f.owner), ListPopularTablesRequestObject{Id: f.ws})
	if err != nil {
		t.Fatal(err)
	}
	got = resp.(ListPopularTables200JSONResponse)
	if len(got) != 1 || got[0].TableId != f.orders {
		t.Fatalf("ungranted table leaked: %+v", got)
	}
}

func TestPopularTables_ForeignWorkspace404(t *testing.T) {
	t.Parallel()
	f := newMetaFixture(t)
	resp, err := f.h.ListPopularTables(f.as(f.stranger), ListPopularTablesRequestObject{Id: f.ws})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := resp.(ListPopularTables404JSONResponse); !ok {
		t.Fatalf("response = %T, want 404", resp)
	}
}
