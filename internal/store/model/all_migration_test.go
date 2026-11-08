package model_test

import (
	"testing"

	"github.com/galileostudio/baleia/internal/store/model"
)

func TestAllModelsMigrate(t *testing.T) {
	t.Parallel()
	models := model.All()
	t.Run("postgres", func(t *testing.T) {
		t.Parallel()
		checkMigration(t, openPostgres(t), models)
	})
	t.Run("mysql", func(t *testing.T) {
		t.Parallel()
		checkMigration(t, openMySQL(t, "BALEIA_TEST_MYSQL_URL"), models)
	})
	t.Run("mariadb", func(t *testing.T) {
		t.Parallel()
		checkMigration(t, openMySQL(t, "BALEIA_TEST_MARIADB_URL"), models)
	})
}
