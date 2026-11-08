package repo

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/store/model"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// TestRetrieverStore covers the GORM replacement for retrievers.sql (#118,
// #774): CRUD, destinations and refresh history.
func TestRetrieverStore(t *testing.T) {
	t.Parallel()
	f := newWorkbenchFixture(t)
	ctx := context.Background()
	r := f.r

	// The fixture binding must look like an Iceberg destination.
	if err := f.gdb.Model(&model.CatalogBinding{}).
		Where("id = ?", string(f.binding)).
		Updates(map[string]any{"connector": "iceberg", "lifecycle_state": "active"}).Error; err != nil {
		t.Fatalf("shape binding: %v", err)
	}

	destinations, err := r.ListRetrieverDestinations(ctx, f.wsID())
	if err != nil {
		t.Fatalf("ListRetrieverDestinations: %v", err)
	}
	if len(destinations) != 1 || destinations[0].BindingID != f.bindingID() {
		t.Fatalf("destinations = %+v", destinations)
	}
	if destinations[0].CatalogID != f.catalogID() || destinations[0].EngineID != f.engineID() || destinations[0].Connector != "iceberg" {
		t.Fatalf("destination = %+v", destinations[0])
	}

	next := pgtype.Timestamptz{Time: time.Now().Add(time.Hour).UTC(), Valid: true}
	created, err := r.CreateRetriever(ctx, db.CreateRetrieverParams{
		WorkspaceID: f.wsID(), CreatedBy: f.ownerID(), Name: "Daily orders",
		DefinitionSql: "select * from orders", DestCatalog: "lake",
		DestNamespace: "reports", DestTable: "orders", DestBindingID: f.bindingID(),
		CronSchedule: "0 3 * * *", RefreshType: "full", NextRefreshAt: next,
	})
	if err != nil {
		t.Fatalf("CreateRetriever: %v", err)
	}
	if created.Status != "active" || created.Name != "Daily orders" || created.DestBindingID != f.bindingID() {
		t.Fatalf("created = %+v", created)
	}
	if !created.NextRefreshAt.Valid || !created.NextRefreshAt.Time.Equal(next.Time) {
		t.Fatalf("next refresh = %v, want %v", created.NextRefreshAt, next.Time)
	}

	got, err := r.GetRetrieverByID(ctx, db.GetRetrieverByIDParams{WorkspaceID: f.wsID(), ID: created.ID})
	if err != nil || got.ID != created.ID {
		t.Fatalf("GetRetrieverByID = %+v, %v", got, err)
	}
	if _, err := r.GetRetrieverByID(ctx, db.GetRetrieverByIDParams{WorkspaceID: PGUUID(NewUUID()), ID: created.ID}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("GetRetrieverByID(other workspace) = %v, want ErrNoRows", err)
	}
	list, err := r.ListRetrieversByWorkspace(ctx, f.wsID())
	if err != nil || len(list) != 1 || list[0].ID != created.ID {
		t.Fatalf("ListRetrieversByWorkspace = %+v, %v", list, err)
	}

	renamed := "Daily orders v2"
	updated, err := r.UpdateRetriever(ctx, db.UpdateRetrieverParams{
		ID: created.ID, WorkspaceID: f.wsID(), Name: renamed, DefinitionSql: "select 1",
		DestCatalog: "lake", DestNamespace: "reports", DestTable: "orders2",
		DestBindingID: f.bindingID(), CronSchedule: "0 4 * * *", RefreshType: "full",
		Status: "paused", NextRefreshAt: next,
	})
	if err != nil {
		t.Fatalf("UpdateRetriever: %v", err)
	}
	if updated.Name != renamed || updated.DestTable != "orders2" || updated.Status != "paused" || updated.CronSchedule != "0 4 * * *" {
		t.Fatalf("updated = %+v", updated)
	}
	if _, err := r.UpdateRetriever(ctx, db.UpdateRetrieverParams{ID: PGUUID(NewUUID()), WorkspaceID: f.wsID()}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("UpdateRetriever(missing) = %v, want ErrNoRows", err)
	}

	// Refresh history and its latest status.
	finished := time.Now().UTC()
	history := []model.RetrieverRefreshHistory{
		{ID: model.UUID(NewUUID()), RetrieverID: model.UUID(UUIDString(created.ID)), TemporalWorkflowID: "run-1",
			RefreshType: "full", Status: "failed", ErrorCode: strPtr("engine_error"), ErrorMessage: strPtr("boom"),
			StartedAt: finished.Add(-time.Hour), FinishedAt: &finished},
		{ID: model.UUID(NewUUID()), RetrieverID: model.UUID(UUIDString(created.ID)), TemporalWorkflowID: "run-2",
			RefreshType: "full", Status: "finished", StartedAt: finished},
	}
	if err := f.gdb.Create(&history).Error; err != nil {
		t.Fatalf("create history: %v", err)
	}
	runs, err := r.ListRetrieverRefreshHistory(ctx, db.ListRetrieverRefreshHistoryParams{RetrieverID: created.ID, Limit: 10})
	if err != nil || len(runs) != 2 || runs[0].Status != "finished" || runs[1].Status != "failed" {
		t.Fatalf("history = %+v, %v", runs, err)
	}
	latest, err := r.LatestRetrieverRefresh(ctx, created.ID)
	if err != nil || latest.Status != "finished" {
		t.Fatalf("LatestRetrieverRefresh = %+v, %v", latest, err)
	}
	if _, err := r.LatestRetrieverRefresh(ctx, PGUUID(NewUUID())); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("LatestRetrieverRefresh(none) = %v, want ErrNoRows", err)
	}

	if n, err := r.DeleteRetriever(ctx, db.DeleteRetrieverParams{ID: created.ID, WorkspaceID: f.wsID()}); err != nil || n != 1 {
		t.Fatalf("DeleteRetriever = %d, %v", n, err)
	}
	if n, err := r.DeleteRetriever(ctx, db.DeleteRetrieverParams{ID: created.ID, WorkspaceID: f.wsID()}); err != nil || n != 0 {
		t.Fatalf("DeleteRetriever(again) = %d, %v", n, err)
	}
	if _, err := r.GetRetrieverByID(ctx, db.GetRetrieverByIDParams{WorkspaceID: f.wsID(), ID: created.ID}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("GetRetrieverByID(deleted) = %v, want ErrNoRows", err)
	}
	runs, err = r.ListRetrieverRefreshHistory(ctx, db.ListRetrieverRefreshHistoryParams{RetrieverID: created.ID, Limit: 10})
	if err != nil || len(runs) != 0 {
		t.Fatalf("history after delete = %+v, %v", runs, err)
	}
	if _, err := r.LatestRetrieverRefresh(ctx, created.ID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("LatestRetrieverRefresh(deleted) = %v, want ErrNoRows", err)
	}
}
