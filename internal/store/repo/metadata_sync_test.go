package repo

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/jackc/pgx/v5/pgtype"
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
	if err := f.r.TxMetadata(ctx, pgtype.UUID{}, func(tx *Repo) error { return nil }); !errors.Is(err, ErrMetadataSyncTakenOver) {
		t.Fatalf("TxMetadata(invalid run) = %v, want ErrMetadataSyncTakenOver", err)
	}
}
