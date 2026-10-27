package activities

import (
	"context"

	"github.com/galileostudio/baleia/internal/audit"
	"github.com/galileostudio/baleia/internal/bindings"
	"github.com/galileostudio/baleia/internal/bindings/stored"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/queryengine"
	"github.com/galileostudio/baleia/internal/temporal/schedules"
	"github.com/jackc/pgx/v5/pgtype"
	"go.temporal.io/sdk/activity"
)

// engineReconciler reconciles one federated engine's managed bindings
// (bindings.Reconciler); it is wired in cmd/worker, where the binder is.
type engineReconciler func(ctx context.Context, eng db.Engine, clusterID pgtype.UUID, dryRun bool, onAction func(context.Context, bindings.Action)) (bindings.Report, error)

// SetEngineReconciler wires binding reconciliation (#37).
func (a *Activities) SetEngineReconciler(r engineReconciler) { a.reconcileEngine = r }

// ReconcileCatalogsParams says whether the run applies changes.
type ReconcileCatalogsParams struct {
	DryRun bool
}

// ReconcileCatalogsResult counts what the run found across engines.
type ReconcileCatalogsResult struct {
	Engines  int
	Mutating int
	Adopted  int
	Failed   int
}

// ReconcileCatalogs reconciles every enabled federated engine (schedule
// catalog-reconcile, opt-in). Each engine's run is persisted; one failing
// engine never stops the others.
func ReconcileCatalogs(ctx context.Context, p ReconcileCatalogsParams) (*ReconcileCatalogsResult, error) {
	res, err := defaultActivities.reconcileAll(ctx, p.DryRun, func(n int) { activity.RecordHeartbeat(ctx, n) })
	defaultActivities.passDone(schedules.CatalogReconcile, err)
	return res, err
}

// ReconcileAtBoot runs one dry reconcile of every federated engine and logs
// what it found (counts only), so operators notice drift.
func (a *Activities) ReconcileAtBoot(ctx context.Context) {
	res, err := a.reconcileAll(ctx, true, nil)
	if err != nil {
		a.logger.WarnContext(ctx, "reconcile dry run at boot", "err", err)
		return
	}
	a.logger.InfoContext(ctx, "reconcile dry run at boot", "engines", res.Engines, "changes_needed", res.Mutating, "adopted", res.Adopted, "failed", res.Failed)
}

func (a *Activities) reconcileAll(ctx context.Context, dryRun bool, beat func(int)) (*ReconcileCatalogsResult, error) {
	res := &ReconcileCatalogsResult{}
	if a.reconcileEngine == nil {
		return res, nil
	}
	rows, err := a.queries.ListEnabledEnginesWithConnection(ctx)
	if err != nil {
		return nil, err
	}
	actx := audit.WithService(ctx, "reconciler")
	onAction := func(_ context.Context, act bindings.Action) {
		ev := audit.Event{Action: audit.CatalogReconcile, ResourceType: "engine",
			Changes: map[string]audit.Change{"catalog": {To: act.Catalog}, "kind": {To: act.Kind}, "reason": {To: act.Reason}}}
		// Audit is best effort; Record logs and counts its own failures.
		if act.Result == bindings.ResultFailed {
			_ = a.audit.Record(actx, ev.Failed(audit.ReasonEngineError))
			return
		}
		_ = a.audit.Record(actx, ev.Allowed())
	}
	for _, r := range rows {
		if r.Engine.Class != string(queryengine.ClassFederated) || !r.ClusterID.Valid {
			continue
		}
		res.Engines++
		rep, runErr := a.reconcileEngine(ctx, r.Engine, r.ClusterID, dryRun, onAction)
		if _, err := stored.SaveRun(context.WithoutCancel(ctx), a.queries, r.Engine.ID, rep, runErr); err != nil {
			a.logger.WarnContext(ctx, "save reconcile run", "engine", r.Engine.Name, "err", err)
		}
		if runErr != nil {
			res.Failed++
			a.logger.WarnContext(ctx, "reconcile engine", "engine", r.Engine.Name, "err", runErr)
		}
		res.Mutating += rep.Mutating()
		res.Adopted += len(rep.Adopted)
		if beat != nil {
			beat(res.Engines)
		}
	}
	return res, nil
}
