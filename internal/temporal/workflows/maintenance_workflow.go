package workflows

import (
	"errors"
	"time"

	"github.com/galileostudio/baleia/internal/temporal/activities"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// MaintenanceRunWorkflow runs the selected Iceberg maintenance operations for
// one table and records the run to completion (#117). It is separate from the
// legacy LensMaintenanceWorkflow so in-flight histories keep replaying.
//
// The workflow closes the run itself once the activity returns, whatever the
// outcome: an activity that times out, loses its worker or cannot find its row
// would otherwise leave the run "running" and block the table.
func MaintenanceRunWorkflow(ctx workflow.Context, runID, engineID, catalog, namespace, table, retention string, operations []string) error {
	closesRun := workflow.GetVersion(ctx, "close-run-in-workflow", workflow.DefaultVersion, 1) >= 1
	actCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Minute,
		HeartbeatTimeout:    60 * time.Second,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
	})
	runErr := workflow.ExecuteActivity(actCtx, activities.LensRunMaintenance, activities.LensRunMaintenanceParams{
		RunID:       runID,
		EngineID:    engineID,
		ExecutionID: runID,
		Catalog:     catalog,
		Namespace:   namespace,
		Table:       table,
		Retention:   retention,
		Operations:  operations,
	}).Get(actCtx, nil)
	if !closesRun {
		return runErr
	}

	// Detached, so a cancelled workflow still closes its run.
	closeCtx, cancel := workflow.NewDisconnectedContext(ctx)
	defer cancel()
	closeCtx = workflow.WithActivityOptions(closeCtx, workflow.ActivityOptions{
		StartToCloseTimeout: time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 3},
	})
	params := activities.CloseMaintenanceRunParams{RunID: runID}
	if runErr != nil {
		params.Failure = rootCause(runErr)
	}
	if err := workflow.ExecuteActivity(closeCtx, activities.CloseMaintenanceRun, params).Get(closeCtx, nil); err != nil {
		workflow.GetLogger(ctx).Error("maintenance run was not closed", "run", runID, "err", err)
	}
	return runErr
}

// rootCause is the innermost message of an activity failure, without the
// activity-error envelope that names scheduling ids.
func rootCause(err error) string {
	var app *temporal.ApplicationError
	if errors.As(err, &app) {
		return app.Message()
	}
	for errors.Unwrap(err) != nil {
		err = errors.Unwrap(err)
	}
	return err.Error()
}
