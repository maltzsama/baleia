package migrate

import (
	"context"
	"database/sql"
	"log/slog"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

func Up(ctx context.Context, dsn string) error {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return err
	}
	defer db.Close()

	if err := goose.SetDialect("postgres"); err != nil {
		return err
	}

	if err := goose.UpContext(ctx, db, "db/migrations"); err != nil {
		return err
	}

	slog.Info("migrations applied successfully")
	return nil
}
