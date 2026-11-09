package model_test

import (
	"os"
	"testing"

	"github.com/galileostudio/baleia/internal/store"
	"github.com/galileostudio/baleia/internal/store/model"
)

// TestAllModelsMigrate runs the schema twice on the backend named by the single
// BALEIA_TEST_DATABASE_URL (the DSN selects the dialector).
func TestAllModelsMigrate(t *testing.T) {
	t.Parallel()
	dsn := os.Getenv("BALEIA_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("BALEIA_TEST_DATABASE_URL not set")
	}
	backend, err := store.BackendFromDSN(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Run(string(backend), func(t *testing.T) {
		t.Parallel()
		if backend == store.MySQL {
			checkMigration(t, openMySQL(t), model.All())
			return
		}
		checkMigration(t, openPostgres(t), model.All())
	})
}
