package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/galileostudio/baleia/internal/app"
	"github.com/galileostudio/baleia/internal/command"
	"github.com/galileostudio/baleia/internal/config"
	"github.com/galileostudio/baleia/internal/trino"
	"github.com/galileostudio/baleia/internal/trino/monitor"
	"github.com/galileostudio/baleia/internal/webhooks"
	"github.com/galileostudio/baleia/internal/workerapp"
	"github.com/google/uuid"
	"go.temporal.io/sdk/worker"
)

func main() {
	command.Main("worker", run)
}

// run returns instead of exiting so every deferred Close runs.
func run(ctx context.Context, cfg *config.Config, logger *slog.Logger) error {
	core, err := command.OpenCore(ctx, cfg, logger, app.Options{Service: "worker", PoolSize: 10, Logger: logger})
	if err != nil {
		return err
	}
	defer core.Close()

	stopMetrics := workerapp.ServeMetrics(ctx, logger, cfg.WorkerMetricsAddr, core.Metrics, cfg.MetricsToken)
	defer stopMetrics()

	// Lens reads the Trino coordinator's runtime state as the service identity.
	lensMonitor := monitor.New(trino.NewWithHTTPClient(cfg.TrinoURL, cfg.TrinoServiceUser, core.TrinoHTTP))
	w, act, err := workerapp.New(core, cfg, logger, lensMonitor)
	if err != nil {
		return err
	}
	if cfg.WebhooksEnabled && core.Realtime.Enabled() {
		go workerapp.RelayWebhooks(ctx, logger, core.Outbox, core.Realtime)
		go webhooks.NewRelay(logger, core.Realtime, core.StoreRepo.Webhooks, core.Dialer.HTTPClient(15*time.Second), core.Keyring, uuid.NewString(), core.Metrics.Prometheus()).Run(ctx)
	}

	if cfg.SchedulesEnabled {
		if err := workerapp.EnsureSchedules(ctx, core.Temporal, cfg); err != nil {
			return err
		}
		logger.InfoContext(ctx, "temporal schedules ensured")
	}

	logger.InfoContext(ctx, "worker started", "queue", workerapp.TaskQueue, "schedules_enabled", cfg.SchedulesEnabled)
	go workerapp.ServeHealth(ctx, logger, cfg.WorkerHealthPort)

	// One dry reconcile at boot: drift shows up in the log (counts only).
	go func() {
		ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		act.ReconcileAtBoot(ctx)
	}()

	return w.Run(worker.InterruptCh())
}
