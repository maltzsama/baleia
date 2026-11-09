// Package workerapp wires the Temporal worker: its activities, workflows,
// schedules and process listeners. The Trino client behind the Lens monitor is
// built by cmd/worker, the worker's wiring site (ADR 0007).
package workerapp

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/galileostudio/baleia/internal/app"
	auditexport "github.com/galileostudio/baleia/internal/audit/export"
	"github.com/galileostudio/baleia/internal/bindings"
	"github.com/galileostudio/baleia/internal/bindings/stored"
	"github.com/galileostudio/baleia/internal/config"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/metadata"
	"github.com/galileostudio/baleia/internal/obs"
	"github.com/galileostudio/baleia/internal/store/repo"
	"github.com/galileostudio/baleia/internal/temporal/activities"
	"github.com/galileostudio/baleia/internal/temporal/workflows"
	"github.com/galileostudio/baleia/internal/trino/monitor"
	"go.temporal.io/sdk/worker"
)

// TaskQueue is the Temporal task queue every Baleia workflow and activity
// shares.
const TaskQueue = "baleia-task-queue"

// New builds the worker, wires its activities and registers its workflows. The
// Lens monitor is injected because its Trino client belongs to the command's
// wiring site.
func New(core *app.Core, cfg *config.Config, logger *slog.Logger, lens *monitor.Monitor) (worker.Worker, *activities.Activities, error) {
	w := worker.New(core.Temporal, TaskQueue, worker.Options{Interceptors: obs.WorkerInterceptors()})
	act, err := wireActivities(core, cfg, logger, lens)
	if err != nil {
		return nil, nil, err
	}
	activities.Register(w, act)
	registerWorkflows(w)
	return w, act, nil
}

// wireActivities builds and configures the worker's activities.
func wireActivities(core *app.Core, cfg *config.Config, logger *slog.Logger, lens *monitor.Monitor) (*activities.Activities, error) {
	reg := core.Metrics
	act := activities.New(logger, core.Notifier, core.StoreRepo, cfg, core.Keyring, core.Connections, core.Engines, lens, core.Storage, core.Dialects)
	act.SetStore(core.StoreRepo)
	act.SetMetrics(reg)
	act.SetTemporal(core.Temporal)
	r := core.StoreRepo
	act.SetMetadataSyncer(metadata.NewSyncer(r, func(ctx context.Context, runID db.UUID, fn func(metadata.Store) error) error {
		return r.TxMetadata(ctx, runID, func(tx *repo.Repo) error { return fn(tx) })
	}, metadata.NewSources(r, core.Keyring, core.Connections, core.Engines)))
	// The dispatcher observes federated engines' catalogs (adopting the ones
	// Baleia did not create) with the binder, as the catalog DDL identity.
	binder := core.Binder
	act.SetEngineObserver(func(ctx context.Context, eng db.Engine, clusterID db.UUID) error {
		q := core.StoreRepo
		l := bindings.Lifecycle{Store: q, Binder: binder.WithStore(q), Engine: bindings.EngineRef{ID: eng.ID, ClusterID: clusterID}, ConnectionID: eng.ConnectionID}
		if _, err := l.Observe(ctx); err != nil {
			return err
		}
		_, err := q.DisableMissingAdoptedBindings(ctx, eng.ID)
		return err
	})
	reg.WatchMetadataStaleness(logger, core.Queries.MaxMetadataStalenessSeconds)
	// Binding reconciliation (#37): managed drift only; adopted catalogs are
	// reported, never changed.
	act.SetEphemeralSweeper(func(ctx context.Context) (int, error) { return binder.SweepEphemeral(ctx, core.Queries) })
	projector := stored.Projector{Store: core.Queries, Keyring: core.Keyring, Providers: core.Connections, Binder: binder}
	act.SetEngineReconciler(func(ctx context.Context, eng db.Engine, clusterID db.UUID, dryRun bool, onAction func(context.Context, bindings.Action)) (bindings.Report, error) {
		q := core.StoreRepo
		r := bindings.Reconciler{
			Lifecycle:  bindings.Lifecycle{Store: q, Binder: binder.WithStore(q), Engine: bindings.EngineRef{ID: eng.ID, ClusterID: clusterID}, ConnectionID: eng.ConnectionID},
			Store:      q,
			Project:    projector.Project,
			MaxActions: cfg.ReconcileMaxActions,
			OnAction:   onAction,
		}
		return r.Run(ctx, dryRun)
	})
	act.SetAudit(core.Audit)

	// Audit export to OpenSearch (#50): disabled unless a URL is configured.
	if cfg.AuditExportURL != "" {
		sink, err := auditexport.NewHTTPBulk(auditexport.Config{
			URL: cfg.AuditExportURL, Index: cfg.AuditExportIndex, Auth: cfg.AuditExportAuth,
			User: cfg.AuditExportUser, Password: cfg.AuditExportPassword, Token: cfg.AuditExportToken, CAFile: cfg.AuditExportCAFile,
		}, obs.WrapHTTPClient(&http.Client{Timeout: 60 * time.Second}))
		if err != nil {
			return nil, fmt.Errorf("audit export sink: %w", err)
		}
		pg := auditexport.Postgres{Queries: core.StoreRepo}
		// Registered once: calling AuditExport() per lag report would register
		// the collectors again and panic.
		exportMetrics := reg.AuditExport()
		act.SetAuditExporter(&auditexport.Exporter{
			Source: pg, Cursor: pg, Sink: sink,
			Index: cfg.AuditExportIndex, Batch: cfg.AuditExportBatch, Lag: auditexport.DefaultSafetyLag,
			OnBatch: exportMetrics.Observe,
			OnLag:   func(t time.Time) { exportMetrics.SetLag(time.Since(t).Seconds()) },
		})
	}
	return act, nil
}

// registerWorkflows registers every workflow the task queue runs.
func registerWorkflows(w worker.Worker) {
	w.RegisterWorkflow(workflows.QueryExecutionWorkflowV3)
	w.RegisterWorkflow(workflows.QueryExecutionWorkflowV4)
	w.RegisterWorkflow(workflows.AdmissionWorkflow)
	w.RegisterWorkflow(workflows.LensQueryPollWorkflow)
	w.RegisterWorkflow(workflows.LensClusterPollWorkflow)
	w.RegisterWorkflow(workflows.LensTableHealthWorkflow)
	w.RegisterWorkflow(workflows.LensMaintenanceWorkflow)
	w.RegisterWorkflow(workflows.MaintenanceRunWorkflow)
	w.RegisterWorkflow(workflows.MaintenanceOperationWorkflow)
	w.RegisterWorkflow(workflows.MaintenanceRoundWorkflow)
	w.RegisterWorkflow(workflows.TableHealthAnalyzeWorkflow)
	w.RegisterWorkflow(workflows.RetrieverDispatchWorkflow)
	w.RegisterWorkflow(workflows.RetrieverRefreshWorkflow)
	w.RegisterWorkflow(workflows.MetadataSyncWorkflow)
	w.RegisterWorkflow(workflows.MetadataSyncDispatchWorkflow)
	w.RegisterWorkflow(workflows.AuditPartitionsWorkflow)
	w.RegisterWorkflow(workflows.AuditExportWorkflow)
	w.RegisterWorkflow(workflows.CatalogReconcileWorkflow)
	w.RegisterWorkflow(workflows.DirectoryReconcileWorkflow)
	w.RegisterWorkflow(workflows.EphemeralCatalogSweepWorkflow)
	w.RegisterWorkflow(workflows.TokenCleanupWorkflow)
	w.RegisterWorkflow(workflows.RefreshTablePopularityWorkflow)
}
