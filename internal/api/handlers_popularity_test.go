package api

import (
	"context"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/store/model"
	"github.com/galileostudio/baleia/internal/store/repo"
)

// TestPopularTables_Refresh counts two finished statements that read orders,
// then lists the table with its count and excludes an ungranted catalog (B7,
// #562).
func TestPopularTables_Refresh(t *testing.T) {
	t.Parallel()
	f := newMetaFixture(t)
	ctx := context.Background()
	for range 2 {
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
			ID: model.UUID(repo.NewUUID()), ExecutionID: execution.ID, Position: 1,
			SQL: "select 1", Status: "finished", StartedAt: now, FinishedAt: &now,
			ResultSets: model.JSON(`[]`),
		}
		if err := f.gdb.Create(&statement).Error; err != nil {
			t.Fatal(err)
		}
		catalog := model.UUID(repo.UUIDString(pgUUIDOf(f.lake)))
		if err := f.gdb.Create(&model.StatementReference{
			StatementID: statement.ID, WorkspaceID: model.UUID(f.ws.String()), CatalogID: &catalog,
			Namespace: "sales", TableNameValue: "orders", Alias: "o", Kind: "read",
		}).Error; err != nil {
			t.Fatal(err)
		}
	}
	q := repo.New(f.gdb)
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
	if err := f.gdb.Create(&model.TablePopularity{
		WorkspaceID: model.UUID(f.ws.String()), CatalogID: model.UUID(repo.UUIDString(pgUUIDOf(f.other))),
		Namespace: "sales", TableNameValue: "orders_secret", Uses: 9, Score: 9, LastUsedAt: time.Now(),
	}).Error; err != nil {
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
