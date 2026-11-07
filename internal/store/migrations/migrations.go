// Package migrations holds Baleia's store schema as Bun Go migrations
// (docs/adr/0020). Each file registers one version with the same numeric id as
// the goose migration it replaces, and emits the DDL for the dialect Bun is
// running on.
package migrations

import (
	"context"
	"fmt"
	"strings"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect"
	"github.com/uptrace/bun/migrate"
)

// Migrations is the ordered set Bun applies and rolls back.
var Migrations = migrate.NewMigrations()

// dialectDDL runs the statement for the dialect Bun is connected to.
func dialectDDL(ctx context.Context, db *bun.DB, postgres, mysql string) error {
	var query string
	switch db.Dialect().Name() {
	case dialect.PG:
		query = postgres
	case dialect.MySQL:
		query = mysql
	default:
		return fmt.Errorf("migrations: unsupported dialect %s", db.Dialect().Name())
	}
	if strings.TrimSpace(query) == "" {
		return nil
	}
	_, err := db.ExecContext(ctx, query)
	return err
}
