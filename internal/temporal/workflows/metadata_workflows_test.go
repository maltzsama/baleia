package workflows

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/temporal/activities"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
)

// TestMetadataSyncWorkflow: one pass of the named catalog, retried up to 3
// times (a pass is idempotent).
func TestMetadataSyncWorkflow(t *testing.T) {
	t.Parallel()
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	var calls int32
	env.OnActivity(activities.SyncCatalogMetadata, mock.Anything, mock.MatchedBy(func(p activities.SyncCatalogMetadataParams) bool {
		return p.CatalogID == "cat-1"
	})).Return(func(context.Context, activities.SyncCatalogMetadataParams) (*activities.SyncCatalogMetadataResult, error) {
		if atomic.AddInt32(&calls, 1) < 3 {
			return nil, errors.New("catalog manager down")
		}
		return &activities.SyncCatalogMetadataResult{Path: "provider", TablesSeen: 6}, nil
	})
	env.ExecuteWorkflow(MetadataSyncWorkflow, MetadataSyncInput{CatalogID: "cat-1"})
	require.NoError(t, env.GetWorkflowError())
	var res activities.SyncCatalogMetadataResult
	require.NoError(t, env.GetWorkflowResult(&res))
	require.Equal(t, 6, res.TablesSeen)
	require.Equal(t, int32(3), atomic.LoadInt32(&calls))
	require.Equal(t, "metadata-sync-cat-1", MetadataSyncWorkflowID("cat-1"))
}

// TestMetadataDispatch_BoundedConcurrency: engines are refreshed first, then
// every syncable catalog is synced as a child, never more than 4 at once.
func TestMetadataDispatch_BoundedConcurrency(t *testing.T) {
	t.Parallel()
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(MetadataSyncWorkflow)
	var order []string
	env.OnActivity(activities.RefreshEngineCatalogs, mock.Anything).Return(func(context.Context) (*activities.RefreshEngineCatalogsResult, error) {
		order = append(order, "refresh")
		return &activities.RefreshEngineCatalogsResult{}, nil
	})
	ids := make([]string, 10)
	for i := range ids {
		ids[i] = string(rune('a' + i))
	}
	env.OnActivity(activities.ListSyncableCatalogs, mock.Anything).Return(func(context.Context) ([]string, error) {
		order = append(order, "list")
		return ids, nil
	})
	var mu sync.Mutex
	var inFlight, peak, total int32
	env.OnActivity(activities.SyncCatalogMetadata, mock.Anything, mock.Anything).Return(func(context.Context, activities.SyncCatalogMetadataParams) (*activities.SyncCatalogMetadataResult, error) {
		mu.Lock()
		inFlight++
		peak = max(peak, inFlight)
		mu.Unlock()
		time.Sleep(20 * time.Millisecond)
		mu.Lock()
		inFlight--
		total++
		mu.Unlock()
		return &activities.SyncCatalogMetadataResult{}, nil
	})
	env.OnActivity(activities.SchedulePassSucceeded, mock.Anything, mock.Anything).Return(nil)
	env.ExecuteWorkflow(MetadataSyncDispatchWorkflow)
	require.NoError(t, env.GetWorkflowError())
	require.Equal(t, []string{"refresh", "list"}, order)
	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, int32(10), total, "every catalog is synced")
	require.LessOrEqual(t, peak, int32(maxMetadataSyncChildren))
	require.Greater(t, peak, int32(1), "children run concurrently")
}

// TestMetadataDispatch_RefreshFailureStillSyncs: a failed engine refresh
// does not stop the known catalogs from syncing; a failed child does not
// fail the pass.
func TestMetadataDispatch_RefreshFailureStillSyncs(t *testing.T) {
	t.Parallel()
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(MetadataSyncWorkflow)
	env.OnActivity(activities.RefreshEngineCatalogs, mock.Anything).Return(nil, errors.New("db down"))
	env.OnActivity(activities.ListSyncableCatalogs, mock.Anything).Return([]string{"a", "b"}, nil)
	env.OnActivity(activities.SyncCatalogMetadata, mock.Anything, mock.MatchedBy(func(p activities.SyncCatalogMetadataParams) bool { return p.CatalogID == "a" })).
		Return(nil, temporal.NewNonRetryableApplicationError("gone", "not_found", nil))
	env.OnActivity(activities.SyncCatalogMetadata, mock.Anything, mock.MatchedBy(func(p activities.SyncCatalogMetadataParams) bool { return p.CatalogID == "b" })).
		Return(&activities.SyncCatalogMetadataResult{}, nil).Once()
	env.OnActivity(activities.SchedulePassSucceeded, mock.Anything, mock.Anything).Return(nil).Once()
	env.ExecuteWorkflow(MetadataSyncDispatchWorkflow)
	require.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

// TestMetadataSync_NoDuplicate: a catalog whose sync is already running is
// not synced twice: the child's workflow id is the catalog's.
func TestMetadataSync_NoDuplicate(t *testing.T) {
	t.Parallel()
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(MetadataSyncWorkflow)
	env.OnActivity(activities.RefreshEngineCatalogs, mock.Anything).Return(&activities.RefreshEngineCatalogsResult{}, nil)
	env.OnActivity(activities.ListSyncableCatalogs, mock.Anything).Return([]string{"a", "a"}, nil)
	var calls int32
	env.OnActivity(activities.SyncCatalogMetadata, mock.Anything, mock.Anything).Return(func(context.Context, activities.SyncCatalogMetadataParams) (*activities.SyncCatalogMetadataResult, error) {
		atomic.AddInt32(&calls, 1)
		time.Sleep(50 * time.Millisecond)
		return &activities.SyncCatalogMetadataResult{}, nil
	})
	env.OnActivity(activities.SchedulePassSucceeded, mock.Anything, mock.Anything).Return(nil)
	env.ExecuteWorkflow(MetadataSyncDispatchWorkflow)
	require.NoError(t, env.GetWorkflowError())
	require.Equal(t, int32(1), atomic.LoadInt32(&calls))
}
