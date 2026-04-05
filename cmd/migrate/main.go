package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"github.com/pressly/goose/v3"
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/galileostudio/baleia/internal/config"
)

func main() {
	var command string
	flag.StringVar(&command, "cmd", "up", "goose command: up, down, status, reset, version")
	flag.Parse()

	cfg := config.Load()

	db, err := sql.Open("pgx", cfg.DatabaseURL)
	if err != nil {
		slog.Error("failed to open database", "err", err)
		os.Exit(1)
	}
	defer db.Close()

	if err := goose.SetDialect("postgres"); err != nil {
		slog.Error("failed to set dialect", "err", err)
		os.Exit(1)
	}

	ctx := context.Background()

	switch command {
	case "up":
		err = goose.UpContext(ctx, db, "db/migrations")
	case "down":
		err = goose.DownContext(ctx, db, "db/migrations")
	case "status":
		err = goose.StatusContext(ctx, db, "db/migrations")
	case "reset":
		err = goose.ResetContext(ctx, db, "db/migrations")
	case "version":
		err = goose.VersionContext(ctx, db, "db/migrations")
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", command)
		os.Exit(1)
	}

	if err != nil {
		slog.Error("migration error", "cmd", command, "err", err)
		os.Exit(1)
	}
}