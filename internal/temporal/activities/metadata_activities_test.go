package activities

import (
	"context"
	"errors"
	"testing"

	"github.com/galileostudio/baleia/internal/metadata"
	"github.com/jackc/pgx/v5/pgtype"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
)

type fakeSyncer struct{ err error }

func (f fakeSyncer) Sync(context.Context, pgtype.UUID, func(int)) (metadata.RunStats, error) {
	return metadata.RunStats{Path: metadata.PathProvider, TablesSeen: 2}, f.err
}

// TestSyncCatalogMetadata_Errors: a catalog that is gone or already syncing
// is not retried; other failures are.
func TestSyncCatalogMetadata_Errors(t *testing.T) {
	const id = "0b6d1c1e-5a4f-4a5e-9d7c-2f1e3a4b5c6d"
	for err, nonRetryable := range map[error]bool{
		metadata.ErrSyncInProgress:  true,
		metadata.ErrCatalogNotFound: true,
		errors.New("network"):       false,
	} {
		a := &Activities{metadata: fakeSyncer{err: err}}
		_, got := a.syncCatalogMetadata(context.Background(), SyncCatalogMetadataParams{CatalogID: id})
		var appErr *temporal.ApplicationError
		if isApp := errors.As(got, &appErr) && appErr.NonRetryable(); isApp != nonRetryable {
			t.Errorf("%v: non-retryable = %v, want %v", err, isApp, nonRetryable)
		}
	}
	before := defaultActivities
	defer func() { defaultActivities = before }()
	defaultActivities = &Activities{metadata: fakeSyncer{}}
	ts := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
	ts.RegisterActivity(SyncCatalogMetadata)
	v, err := ts.ExecuteActivity(SyncCatalogMetadata, SyncCatalogMetadataParams{CatalogID: id})
	if err != nil {
		t.Fatal(err)
	}
	var res SyncCatalogMetadataResult
	if err := v.Get(&res); err != nil || res.TablesSeen != 2 || res.Path != metadata.PathProvider {
		t.Fatalf("%+v, %v", res, err)
	}
}
