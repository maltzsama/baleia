package store

import (
	"context"
	"fmt"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/migrate"
)

// Apply runs the pending migrations and returns the names applied in this run
// (empty when the schema was already current).
func Apply(ctx context.Context, db *bun.DB, migrations *migrate.Migrations) ([]string, error) {
	migrator := migrate.NewMigrator(db, migrations)
	if err := migrator.Init(ctx); err != nil {
		return nil, fmt.Errorf("store: migration init: %w", err)
	}
	group, err := migrator.Migrate(ctx)
	if err != nil {
		return nil, fmt.Errorf("store: migrate: %w", err)
	}
	return names(group), nil
}

// Rollback rolls back the last applied migration group and returns the names
// rolled back.
func Rollback(ctx context.Context, db *bun.DB, migrations *migrate.Migrations) ([]string, error) {
	migrator := migrate.NewMigrator(db, migrations)
	if err := migrator.Init(ctx); err != nil {
		return nil, fmt.Errorf("store: migration init: %w", err)
	}
	group, err := migrator.Rollback(ctx)
	if err != nil {
		return nil, fmt.Errorf("store: rollback: %w", err)
	}
	return names(group), nil
}

func names(group *migrate.MigrationGroup) []string {
	if group == nil {
		return nil
	}
	out := make([]string, 0, len(group.Migrations))
	for _, m := range group.Migrations {
		out = append(out, m.Name)
	}
	return out
}
