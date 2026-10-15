package workflows

import (
	"time"

	"github.com/galileostudio/baleia/internal/temporal/activities"
	"github.com/galileostudio/baleia/internal/temporal/schedules"
	enumspb "go.temporal.io/api/enums/v1"
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

// maxMetadataSyncChildren bounds the catalogs a dispatcher pass syncs at
// once.
const maxMetadataSyncChildren = 4

// MetadataSyncDispatchWorkflow is the scheduled metadata pass (#17, schedule
// metadata-sync): it refreshes every enabled engine's catalogs (native
// databases, adopted Trino catalogs), then syncs every syncable catalog as a
// child MetadataSyncWorkflow, at most maxMetadataSyncChildren at a time. A
// catalog whose sync is already running (on demand, or a slow earlier pass)
// is skipped, never duplicated: the child's workflow id is the catalog's.
// Children are abandoned, not cancelled, if the dispatcher ends first.
func MetadataSyncDispatchWorkflow(ctx workflow.Context) error {
	logger := workflow.GetLogger(ctx)
	actx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 10 * time.Minute,
		HeartbeatTimeout:    2 * time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 3},
	})
	// A failed refresh leaves the known catalogs to sync: log and go on.
	if err := workflow.ExecuteActivity(actx, activities.RefreshEngineCatalogs).Get(actx, nil); err != nil {
		logger.Warn("refresh engine catalogs", "err", err)
	}
	var ids []string
	if err := workflow.ExecuteActivity(actx, activities.ListSyncableCatalogs).Get(actx, &ids); err != nil {
		return err
	}

	sel := workflow.NewSelector(ctx)
	inFlight := 0
	for _, id := range ids {
		if inFlight == maxMetadataSyncChildren {
			sel.Select(ctx)
			inFlight--
		}
		cctx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
			WorkflowID:            MetadataSyncWorkflowID(id),
			WorkflowIDReusePolicy: enumspb.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE,
			ParentClosePolicy:     enumspb.PARENT_CLOSE_POLICY_ABANDON,
		})
		child := workflow.ExecuteChildWorkflow(cctx, MetadataSyncWorkflow, MetadataSyncInput{CatalogID: id})
		var exec workflow.Execution
		if err := child.GetChildWorkflowExecution().Get(ctx, &exec); err != nil {
			// Already running: that pass is this catalog's pass.
			if !temporal.IsWorkflowExecutionAlreadyStartedError(err) {
				logger.Warn("start metadata sync", "catalog_id", id, "err", err)
			}
			continue
		}
		inFlight++
		catalogID := id
		sel.AddFuture(child, func(f workflow.Future) {
			if err := f.Get(ctx, nil); err != nil {
				logger.Warn("metadata sync failed", "catalog_id", catalogID, "err", err)
			}
		})
	}
	for ; inFlight > 0; inFlight-- {
		sel.Select(ctx)
	}
	passSucceeded(ctx, schedules.MetadataSync)
	return nil
}
