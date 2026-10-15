package workflows

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/galileostudio/baleia/internal/temporal/activities"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
)

// TestMetadataSyncWorkflow: one pass of the named catalog, retried up to 3
// times (a pass is idempotent).
func TestMetadataSyncWorkflow(t *testing.T) {
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
