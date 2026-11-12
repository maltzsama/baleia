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

// TestListMetadataTablesPage_UnsupportedReason: a table the source lists but
// cannot read carries its reason in the page row; a readable one, and one whose
// stored properties are not the expected shape, carry none (#977).
func TestListMetadataTablesPage_UnsupportedReason(t *testing.T) {
	t.Parallel()
	f := newWorkbenchFixture(t)
	ctx := context.Background()
	ns := []string{"s"}
	for name, props := range map[string]string{
		"readable":    `{"format":"iceberg"}`,
		"unsupported": `{"unsupported_code":"table_kind","unsupported_detail":"TABLE_DB_STORAGE"}`,
		"refused":     `{"unsupported_code":"refused"}`,
		"malformed":   `["not","an","object"]`,
	} {
		if _, err := f.r.UpsertMetadataTable(ctx, db.UpsertMetadataTableParams{
			CatalogID: f.catalogPG(), Namespace: ns, Name: name, Kind: "table",
			Fingerprint: "fp-" + name, Properties: []byte(props),
		}); err != nil {
			t.Fatalf("UpsertMetadataTable(%s): %v", name, err)
		}
	}

	page, err := f.r.ListMetadataTablesPage(ctx, db.ListMetadataTablesPageParams{CatalogID: f.catalogPG(), Namespace: ns, Lim: 10})
	if err != nil {
		t.Fatalf("ListMetadataTablesPage: %v", err)
	}
	got := map[string][2]string{}
	for _, row := range page {
		got[row.Name] = [2]string{row.UnsupportedCode, row.UnsupportedDetail}
	}
	want := map[string][2]string{
		"readable":    {"", ""},
		"unsupported": {"table_kind", "TABLE_DB_STORAGE"},
		"refused":     {"refused", ""},
		"malformed":   {"", ""},
	}
	for name, w := range want {
		if got[name] != w {
			t.Errorf("table %q reason = %v, want %v", name, got[name], w)
		}
	}
}
