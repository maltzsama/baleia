package migrate

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"github.com/galileostudio/baleia/internal/store/migrations"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	bunmigrate "github.com/uptrace/bun/migrate"
)

func bunOver(db *sql.DB) *bun.DB {
	return bun.NewDB(db, pgdialect.New())
}

// upTo applies the Bun migrations up to version, one group per migration so a
// later downTo can roll them back one at a time.
func upTo(t *testing.T, db *sql.DB, _ string, version int64) {
	t.Helper()
	ctx := context.Background()
	bdb := bunOver(db)
	target := fmt.Sprintf("%05d", version)
	for _, m := range migrations.Migrations.Sorted() {
		if m.Name > target {
			break
		}
		single := bunmigrate.NewMigrations()
		single.Add(m)
		migrator := bunmigrate.NewMigrator(bdb, single)
		if err := migrator.Init(ctx); err != nil {
			t.Fatalf("init migrator: %v", err)
		}
		if _, err := migrator.Migrate(ctx); err != nil {
			t.Fatalf("migrate to %s: %v", m.Name, err)
		}
	}
}

// downTo rolls back until the highest applied migration is <= version.
func downTo(t *testing.T, db *sql.DB, _ string, version int64) {
	t.Helper()
	ctx := context.Background()
	bdb := bunOver(db)
	target := fmt.Sprintf("%05d", version)
	for {
		migrator := bunmigrate.NewMigrator(bdb, migrations.Migrations)
		if err := migrator.Init(ctx); err != nil {
			t.Fatalf("init migrator: %v", err)
		}
		applied, err := migrator.MigrationsWithStatus(ctx)
		if err != nil {
			t.Fatalf("status: %v", err)
		}
		highest := ""
		for _, m := range applied {
			if !m.MigratedAt.IsZero() {
				highest = m.Name
			}
		}
		if highest == "" || highest <= target {
			return
		}
		if _, err := migrator.Rollback(ctx); err != nil {
			t.Fatalf("rollback: %v", err)
		}
	}
}
