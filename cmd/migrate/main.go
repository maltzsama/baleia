package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"github.com/galileostudio/baleia/internal/config"
	"github.com/galileostudio/baleia/internal/obs"
)

func main() {
	var command, before string
	var dryRun bool
	flag.StringVar(&command, "cmd", "store-up", "command: store-up, store-down, store-status (GORM schema migrations, ADR 0020); credentials (re-seal legacy credentials), oidc-providers (seal provider configs) or audit-prune (drop the gated audit partitions before -before)")
	flag.StringVar(&before, "before", "", "audit-prune: drop the audit partitions before this day (YYYY-MM-DD, or YYYY-MM for a month start)")
	flag.BoolVar(&dryRun, "dry-run", false, "audit-prune: report the partitions that would be dropped without dropping them")
	flag.Parse()

	cfg := config.Load()
	level, _ := cfg.SlogLevel()
	logger := obs.NewLogger(os.Stdout, level)
	slog.SetDefault(logger)
	ctx := context.Background()

	var err error
	switch command {
	case "store-up":
		err = runStoreUp(ctx, logger, cfg)
	case "store-down":
		err = runStoreDown(ctx, logger, cfg)
	case "store-status":
		err = runStoreStatus(ctx, logger, cfg)
	case "credentials":
		err = migrateCredentials(ctx, logger, cfg)
	case "oidc-providers":
		err = migrateOIDCProviders(ctx, logger, cfg)
	case "audit-prune":
		err = pruneAudit(ctx, logger, cfg, before, dryRun)
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", command)
		os.Exit(1)
	}

	if err != nil {
		logger.ErrorContext(ctx, "migration error", "cmd", command, "err", err)
		os.Exit(1)
	}
}
