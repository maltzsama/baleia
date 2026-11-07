package migrations_test

import (
	"context"
	"os"
	"testing"

	"github.com/galileostudio/baleia/internal/store"
	"github.com/galileostudio/baleia/internal/store/migrations"
	"github.com/galileostudio/baleia/internal/testdb"
)

func TestApplyAllAndRollback(t *testing.T) {
	if os.Getenv("BALEIA_TEST_DATABASE_URL") == "" {
		t.Skip("BALEIA_TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	pool := testdb.NewEmpty(t)
	db, err := store.Open(ctx, pool.Config().ConnString(), store.Options{MaxOpenConns: 2})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = db.Close() }()

	want := len(migrations.Migrations.Sorted())
	applied, err := store.Apply(ctx, db, migrations.Migrations)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(applied) != want {
		t.Fatalf("Apply applied %d migrations, want %d", len(applied), want)
	}

	var users int
	if err := db.NewSelect().TableExpr("users").ColumnExpr("count(*)").Scan(ctx, &users); err != nil {
		t.Fatalf("users table was not created: %v", err)
	}
}
