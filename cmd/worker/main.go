package main

import (
	"log/slog"
	"os"

	"github.com/yourusername/baleia/internal/config"
	"github.com/yourusername/baleia/internal/temporal/workflows"
	"github.com/yourusername/baleia/internal/temporal/activities"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
)

func main() {
	cfg := config.Load()

	c, err := client.Dial(client.Options{HostPort: cfg.TemporalHost})
	if err != nil {
		slog.Error("failed to connect to temporal", "err", err)
		os.Exit(1)
	}
	defer c.Close()

	w := worker.New(c, "baleia-task-queue", worker.Options{})

	workflows.Register(w)
	activities.Register(w)

	if err := w.Run(worker.InterruptCh()); err != nil {
		slog.Error("worker error", "err", err)
		os.Exit(1)
	}
}
