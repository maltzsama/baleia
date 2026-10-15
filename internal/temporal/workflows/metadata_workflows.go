package workflows

import (
	"time"

	"github.com/galileostudio/baleia/internal/temporal/activities"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// MetadataSyncInput names the catalog whose metadata snapshot to refresh.
type MetadataSyncInput struct {
	CatalogID string
}

// MetadataSyncWorkflowID is the workflow id of a catalog's sync: one at a
// time per catalog (the Syncer's lock is the second guard).
func MetadataSyncWorkflowID(catalogID string) string { return "metadata-sync-" + catalogID }

// MetadataSyncWorkflow runs one metadata pass of a catalog (#15). Scheduling
// is #17; for now it starts on demand.
func MetadataSyncWorkflow(ctx workflow.Context, in MetadataSyncInput) (*activities.SyncCatalogMetadataResult, error) {
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: time.Hour,
		HeartbeatTimeout:    time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 3},
	})
	var res activities.SyncCatalogMetadataResult
	if err := workflow.ExecuteActivity(ctx, activities.SyncCatalogMetadata, activities.SyncCatalogMetadataParams{CatalogID: in.CatalogID}).Get(ctx, &res); err != nil {
		return nil, err
	}
	return &res, nil
}
