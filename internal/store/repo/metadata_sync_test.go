package repo

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/db"
)

// TestMetadataSyncFence covers the ownership check every metadata write
// transaction performs: a run that lost its lease cannot mutate the catalog.
func TestMetadataSyncFence(t *testing.T) {
	t.Parallel()
	f := newWorkbenchFixture(t)
	ctx := context.Background()

	run, started, err := f.r.BeginMetadataSyncRun(ctx, f.catalogID(), "provider", time.Minute)
	if err != nil || !started {
		t.Fatalf("BeginMetadataSyncRun = %+v, %v, %v", run, started, err)
	}
	if err := f.r.TxMetadata(ctx, run.ID, func(tx *Repo) error { return nil }); err != nil {
		t.Fatalf("TxMetadata(owner) = %v", err)
	}

	// A takeover marks the old run failed; its writes are then refused.
	if err := f.r.FinishMetadataSyncRun(ctx, db.FinishMetadataSyncRunParams{ID: run.ID, Status: "failed"}); err != nil {
		t.Fatalf("finish run: %v", err)
	}
	if err := f.r.TxMetadata(ctx, run.ID, func(tx *Repo) error { return nil }); !errors.Is(err, ErrMetadataSyncTakenOver) {
		t.Fatalf("TxMetadata(former owner) = %v, want ErrMetadataSyncTakenOver", err)
	}
	if err := f.r.TxMetadata(ctx, db.UUID{}, func(tx *Repo) error { return nil }); !errors.Is(err, ErrMetadataSyncTakenOver) {
		t.Fatalf("TxMetadata(invalid run) = %v, want ErrMetadataSyncTakenOver", err)
	}
}

// TestGetCatalogSyncState_NoFinishedRun: a catalog with no finished run has no
// sync time and no error, and one with a finished run reports it. MAX over no
// rows is NULL, which a plain time.Time cannot scan.
func TestGetCatalogSyncState_NoFinishedRun(t *testing.T) {
	t.Parallel()
	f := newWorkbenchFixture(t)
	ctx := context.Background()

	state, err := f.r.GetCatalogSyncState(ctx, f.catalogPG())
	if err != nil {
		t.Fatalf("GetCatalogSyncState(no runs) = %v", err)
	}
	if state.SyncedAt.Valid {
		t.Errorf("SyncedAt with no runs = %+v, want unset", state.SyncedAt)
	}

	run, started, err := f.r.BeginMetadataSyncRun(ctx, f.catalogID(), "provider", time.Minute)
	if err != nil || !started {
		t.Fatalf("BeginMetadataSyncRun = %+v, %v, %v", run, started, err)
	}
	state, err = f.r.GetCatalogSyncState(ctx, f.catalogPG())
	if err != nil || state.SyncedAt.Valid {
		t.Fatalf("GetCatalogSyncState(running) = %+v, %v; want unset SyncedAt", state, err)
	}

	if err := f.r.FinishMetadataSyncRun(ctx, db.FinishMetadataSyncRunParams{ID: run.ID, Status: "finished"}); err != nil {
		t.Fatalf("finish run: %v", err)
	}
	state, err = f.r.GetCatalogSyncState(ctx, f.catalogPG())
	if err != nil || !state.SyncedAt.Valid {
		t.Fatalf("GetCatalogSyncState(finished) = %+v, %v; want SyncedAt set", state, err)
	}
}
